// Package populate translates a Strapi-style populate selection into the GORM
// association paths a caller should preload.
//
// A Strapi query asks for related records to be loaded alongside the root model
// with repeated populate entries (populate=organization&populate=role) or with
// the wildcard populate=*. Each entry is a relation json-name, optionally written
// in dot notation to reach a nested relation (organization.role). Plan resolves
// every entry against the model's relation whitelist and returns one Spec per
// entry carrying the GORM association name (the related field's exact Go field
// name, dotted for nested paths). The facade consumes the returned specs and
// issues a db.Preload(spec.Association) for each.
//
// # Wildcard
//
// The single entry "*" requests populating all top-level relations. It resolves
// to exactly one Spec with All set to true and an empty Association; the facade
// is expected to translate that into clause.Associations (GORM's "preload every
// association" sentinel) rather than a named Preload. When "*" appears anywhere
// in the input the result is that single wildcard Spec, mirroring the parser,
// which collapses a wildcard populate to {"*"}.
//
// # Security model
//
// Relation names are untrusted input drawn from the query string. Every path
// segment is resolved against schema metadata (the relation whitelist) via
// Model.Relation before any association name is emitted; an unrecognized relation
// yields a wrapped schema.ErrUnknownRelation and is never propagated to GORM. The
// only strings placed in a Spec.Association are Go field names taken from schema
// metadata, never raw query-string tokens.
//
// This package deliberately does not import gorm: it is a pure planner over
// schema metadata so it can be unit-tested without the GORM dependency. The
// translation from []Spec to actual Preload calls lives in the facade.
package populate

import (
	"fmt"
	"strings"

	"github.com/faisalcayunda/strapgorm/schema"
)

// Spec is a single resolved populate directive.
//
// Exactly one of its two shapes is meaningful per value:
//
//   - When All is false, Association holds a GORM association path: a single Go
//     field name ("Organization") for a top-level relation, or a dot-joined chain
//     of Go field names ("User.Role") for a nested relation path. The facade
//     issues db.Preload(Association) for it.
//   - When All is true, Association is empty and the spec requests preloading
//     every top-level relation; the facade maps it to GORM's clause.Associations.
type Spec struct {
	// Association is the GORM association path to preload (empty when All is true).
	Association string
	// All is true when the spec requests preloading all top-level relations,
	// produced from the wildcard "*" populate entry.
	All bool
}

// Plan resolves a list of Strapi-style populate entries into the association
// specs to preload for model m.
//
// Each entry in raw is a relation json-name, optionally dotted to reach a nested
// relation (for example "organization.role"). Blank entries (after trimming) are
// skipped. The wildcard "*" requests populating all top-level relations: when it
// appears anywhere in raw, Plan returns a single Spec{All: true} and ignores any
// other entries, matching the parser's wildcard collapsing.
//
// Every non-wildcard entry is resolved against m's relation whitelist segment by
// segment: each dotted segment must resolve through the current model's relations
// to a Go field name, and resolution then descends into the related model for the
// next segment. The resulting Spec.Association is the dot-joined chain of Go field
// names (the GORM association path). Duplicate entries that resolve to the same
// association path are emitted once, preserving first-appearance order.
//
// An empty raw slice (or one containing only blank entries) yields a nil slice
// and no error, signaling the caller to preload nothing. An unrecognized relation
// in any segment yields a wrapped schema.ErrUnknownRelation; on any error Plan
// returns a nil slice and the error.
func Plan(m *schema.Model, raw []string) ([]Spec, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var specs []Spec
	seen := make(map[string]struct{}, len(raw))

	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// The wildcard collapses the entire selection to "preload all". It takes
		// precedence over any other entry, so return immediately.
		if entry == "*" {
			return []Spec{{All: true}}, nil
		}

		association, err := resolvePath(m, entry)
		if err != nil {
			return nil, err
		}

		if _, dup := seen[association]; dup {
			continue
		}
		seen[association] = struct{}{}
		specs = append(specs, Spec{Association: association})
	}

	return specs, nil
}

// resolvePath resolves a (possibly dotted) relation path against m, returning the
// GORM association path: the dot-joined chain of Go field names. Each segment is
// resolved against the current model's relation whitelist; resolution descends
// into the related model for the next segment. An unknown segment yields a wrapped
// schema.ErrUnknownRelation.
func resolvePath(m *schema.Model, path string) (string, error) {
	current := m
	var goFields []string

	// Split on "." so that a leading, trailing, or doubled dot surfaces as a
	// blank segment (rather than being dropped by a Cut-driven loop that stops at
	// the last separator) and is rejected below.
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			// A blank segment (a leading, trailing, or doubled dot) is not a
			// resolvable relation; reject the whole path rather than dropping it.
			return "", fmt.Errorf("strapgorm/populate: %w: %q", schema.ErrUnknownRelation, path)
		}

		last := i == len(segments)-1

		rel, ok := current.Relation(segment)
		if !ok {
			return "", fmt.Errorf("strapgorm/populate: %w: %q", schema.ErrUnknownRelation, segment)
		}
		goFields = append(goFields, rel.GoField)

		// Only descend into the related model when more segments remain; the leaf
		// segment does not need its related schema resolved.
		if !last {
			related, err := schema.Get(rel.RelatedType)
			if err != nil {
				return "", fmt.Errorf("strapgorm/populate: resolving relation %q: %w", segment, err)
			}
			current = related
		}
	}

	return strings.Join(goFields, "."), nil
}
