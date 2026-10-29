// Package config selects one of the three PostgreSQL databases.
//
//	APP_ENV            development (default) | test | production
//	DEV_DATABASE_URL   default postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_dev?sslmode=disable
//	TEST_DATABASE_URL  default postgres://contractaudit:contractaudit@127.0.0.1:5432/contract_audit_test?sslmode=disable
//	PROD_DATABASE_URL  no default; required only when APP_ENV=production
//
// go run ./cmd/server uses development. Tests use test. Production is never implied.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

const (
	ModeDevelopment = "development"
	ModeTest        = "test"
	ModeProduction  = "production"

	DevDatabaseName  = "contract_audit_dev"
	TestDatabaseName = "contract_audit_test"
	ProdDatabaseName = "contract_audit_prod"

	defaultUser     = "contractaudit"
	defaultPassword = "contractaudit"
	defaultHost     = "127.0.0.1"
	defaultPort     = "5432"
)

var (
	// ErrProductionUnset means APP_ENV=production without PROD_DATABASE_URL.
	ErrProductionUnset = errors.New("production database URL is required")
	// ErrRefusedDatabase means this process would open another environment's database.
	ErrRefusedDatabase = errors.New("database identity is reserved for another environment")
	// ErrUnknownMode means APP_ENV is not development, test, or production.
	ErrUnknownMode = errors.New("unknown application environment")
	// ErrDatabaseURL means a URL cannot be proven to select exactly the identity
	// in its authority and path. Do not include the DSN: it can contain secrets.
	ErrDatabaseURL = errors.New("database URL must be a PostgreSQL URL without identity overrides")
)

// Identity is the host, port, and database name of one DSN.
type Identity struct {
	Host string
	Port string
	Name string
}

// URL renders this identity as a credential-free PostgreSQL target. It never
// includes DSN credentials or query parameters.
func (i Identity) URL() string {
	if i.Host == "" || i.Name == "" {
		return ""
	}
	port := i.Port
	if port == "" {
		port = defaultPort
	}
	return "postgres://" + net.JoinHostPort(i.Host, port) + "/" + i.Name
}

// Environ is the process configuration the selector reads.
type Environ struct {
	AppEnv          string
	DevDatabaseURL  string
	TestDatabaseURL string
	ProdDatabaseURL string
}

// Selection is the database this process may open, plus the other two identities
// used to refuse a shared name.
type Selection struct {
	Mode         string
	DSN          string
	DevDSN       string
	TestDSN      string
	ProdDSN      string
	Database     Identity
	DevDatabase  Identity
	TestDatabase Identity
	ProdDatabase Identity
}

// FromProcess reads APP_ENV and the three DATABASE_URL variables.
func FromProcess() (Selection, error) {
	return Select(Environ{
		AppEnv:          os.Getenv("APP_ENV"),
		DevDatabaseURL:  os.Getenv("DEV_DATABASE_URL"),
		TestDatabaseURL: os.Getenv("TEST_DATABASE_URL"),
		ProdDatabaseURL: os.Getenv("PROD_DATABASE_URL"),
	})
}

// Select picks the database for env. An empty AppEnv is development.
func Select(env Environ) (Selection, error) {
	mode, err := normalizeMode(env.AppEnv)
	if err != nil {
		return Selection{}, err
	}
	sel, err := databaseEnvironments(env)
	if err != nil {
		return Selection{}, err
	}
	sel.Mode = mode
	if err := sel.selectDatabase(); err != nil {
		return Selection{}, err
	}
	return sel, nil
}

func databaseEnvironments(env Environ) (Selection, error) {
	devDSN := first(env.DevDatabaseURL, defaultDSN(DevDatabaseName))
	testDSN := first(env.TestDatabaseURL, defaultDSN(TestDatabaseName))
	prodDSN := strings.TrimSpace(env.ProdDatabaseURL)

	devID, err := ParseDSN(devDSN)
	if err != nil {
		return Selection{}, err
	}
	testID, err := ParseDSN(testDSN)
	if err != nil {
		return Selection{}, err
	}
	prodID := Identity{Host: defaultHost, Port: defaultPort, Name: ProdDatabaseName}
	if prodDSN != "" {
		prodID, err = ParseDSN(prodDSN)
		if err != nil {
			return Selection{}, err
		}
	}

	return Selection{
		DevDSN: devDSN, TestDSN: testDSN, ProdDSN: prodDSN,
		DevDatabase: devID, TestDatabase: testID, ProdDatabase: prodID,
	}, nil
}

func (sel *Selection) selectDatabase() error {
	switch sel.Mode {
	case ModeDevelopment:
		if shared(sel.DevDatabase.Name, sel.TestDatabase.Name, sel.ProdDatabase.Name, TestDatabaseName, ProdDatabaseName) {
			return ErrRefusedDatabase
		}
		sel.DSN, sel.Database = sel.DevDSN, sel.DevDatabase
	case ModeTest:
		if shared(sel.TestDatabase.Name, sel.DevDatabase.Name, sel.ProdDatabase.Name, DevDatabaseName, ProdDatabaseName) {
			return ErrRefusedDatabase
		}
		sel.DSN, sel.Database = sel.TestDSN, sel.TestDatabase
	case ModeProduction:
		if sel.ProdDSN == "" {
			return ErrProductionUnset
		}
		if shared(sel.ProdDatabase.Name, sel.DevDatabase.Name, sel.TestDatabase.Name, DevDatabaseName, TestDatabaseName) {
			return ErrRefusedDatabase
		}
		sel.DSN, sel.Database = sel.ProdDSN, sel.ProdDatabase
	}
	return nil
}

// ParseDSN reads the host, port, and database name. The name is the URL path.
func ParseDSN(raw string) (Identity, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Identity{}, ErrDatabaseURL
	}
	if (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" {
		return Identity{}, ErrDatabaseURL
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || !preservesDatabaseIdentity(query) {
		return Identity{}, ErrDatabaseURL
	}
	return databaseURLIdentity(u)
}

func databaseURLIdentity(u *url.URL) (Identity, error) {
	name := strings.Trim(u.Path, "/")
	host := u.Hostname()
	port := u.Port()
	if host == "" || name == "" || strings.Contains(name, "/") || strings.Contains(host, ",") {
		return Identity{}, ErrDatabaseURL
	}
	if port == "" {
		port = defaultPort
	}
	return Identity{Host: host, Port: port, Name: name}, nil
}

func preservesDatabaseIdentity(query url.Values) bool {
	// pgx lets these settings override the URL authority or path.
	for key := range query {
		switch strings.ToLower(key) {
		case "dbname", "database", "host", "hostaddr", "port", "service", "servicefile":
			return false
		}
	}
	return true
}

func normalizeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "development", "dev":
		return ModeDevelopment, nil
	case "test":
		return ModeTest, nil
	case "production", "prod":
		return ModeProduction, nil
	default:
		return "", ErrUnknownMode
	}
}

func shared(name string, blocked ...string) bool {
	for _, other := range blocked {
		if name == other {
			return true
		}
	}
	return false
}

func first(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func defaultDSN(name string) string {
	return "postgres://" + defaultUser + ":" + defaultPassword + "@" + defaultHost + ":" + defaultPort + "/" + name + "?sslmode=disable"
}
