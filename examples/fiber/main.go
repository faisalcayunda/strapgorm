// Command fiber demonstrates strapgorm behind a Fiber HTTP handler.
//
// GET /users decodes the Strapi query string from the request URL, runs the
// strapgorm builder against a *gorm.DB, and returns a {data, meta} envelope. Try:
//
//	GET /users?filters[name][$contains]=al&sort=name:asc&pagination[page]=1&pagination[pageSize]=10&pagination[withCount]=true
//	GET /users?search=example&fields=id,name,email
package main

import (
	"log"
	"net/url"
	"os"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	strapgorm "github.com/faisalcayunda/strapgorm"
	"github.com/faisalcayunda/strapgorm/parser"
)

// User is the example model. The json tags name fields as they appear in query
// strings and responses; Searchable opts the model into free-text search.
type User struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Name  string `gorm:"size:120;index" json:"name"`
	Email string `gorm:"size:200;uniqueIndex" json:"email"`
	Bio   string `gorm:"type:text" json:"bio"`
}

// Searchable lists the JSON fields matched by the "search"/"_q" parameter.
func (User) Searchable() []string {
	return []string{"name", "email", "bio"}
}

func main() {
	// In a real service, open the same database your application already uses.
	// gorm.io/driver/postgres is pgx/v5-backed, matching a gostarter pool.
	db, err := gorm.Open(postgres.Open(mustEnv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	app := fiber.New()
	app.Get("/users", listUsers(db))

	if err := app.Listen(":8080"); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// listUsers returns a Fiber handler that lists users from a Strapi query string.
//
// Fiber does not expose a *http.Request, so the raw query string is parsed with
// net/url into url.Values before being handed to parser.FromURL.
func listUsers(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		values, err := url.ParseQuery(string(c.Request().URI().QueryString()))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		qp, err := parser.FromURL(values)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		users, meta, err := strapgorm.New[User](db).
			FromParams(qp).
			Paginate(c.UserContext(), qp.Pagination)
		if err != nil {
			// Unknown fields/relations/operators and coercion failures surface
			// here as wrapped errors; treat them as client errors.
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(fiber.Map{"data": users, "meta": meta})
	}
}

// mustEnv reads an environment variable or stops the program if it is unset.
func mustEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
