// Command gin demonstrates strapgorm behind a Gin HTTP handler.
//
// GET /users decodes the Strapi query string from the request URL, runs the
// strapgorm builder against a *gorm.DB, and returns a {data, meta} envelope. Try:
//
//	GET /users?filters[name][$contains]=al&sort=name:asc&pagination[page]=1&pagination[pageSize]=10&pagination[withCount]=true
//	GET /users?search=example&fields=id,name,email
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
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

	r := gin.Default()
	r.GET("/users", listUsers(db))

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// listUsers returns a Gin handler that lists users from a Strapi query string.
func listUsers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		qp, err := parser.FromURL(c.Request.URL.Query())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		users, meta, err := strapgorm.New[User](db).
			FromParams(qp).
			Paginate(c.Request.Context(), qp.Pagination)
		if err != nil {
			// Unknown fields/relations/operators and coercion failures surface
			// here as wrapped errors; treat them as client errors.
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"data": users, "meta": meta})
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
