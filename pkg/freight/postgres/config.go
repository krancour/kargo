package postgres

import "github.com/kelseyhightower/envconfig"

// Config is configuration for the Postgres implementation of freight.Store.
type Config struct {
	// URL is the connection string for the Postgres database.
	URL string `envconfig:"DATABASE_URL" required:"true"`
}

// ConfigFromEnv returns a Config populated from environment variables.
func ConfigFromEnv() Config {
	cfg := Config{}
	envconfig.MustProcess("", &cfg)
	return cfg
}
