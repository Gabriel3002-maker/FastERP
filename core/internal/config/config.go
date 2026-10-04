package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port             int
	DatabaseURL      string
	JWTSecret        string
	JWTRefreshSecret string
	ModulesDir       string
	UploadDir        string
	StaticDir        string
	TemplatesDir     string
	CORSOrigins      string
	RateLimit        int
	AuthRateLimit    int
	MaxOpenConns     int
	MaxIdleConns     int
	Seed             bool
	Dev              bool
	TrustedProxies   []string
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("[Config] No .env file found, using environment variables")
	}
	return &Config{
		Port:             getEnvInt("FASTERP_PORT", 7071),
		DatabaseURL:      getEnv("FASTERP_DATABASE_URL", "postgres://fasterp:fasterp@localhost:5456/fasterp?sslmode=disable"),
		JWTSecret:        getEnv("FASTERP_JWT_SECRET", "change-me-in-production"),
		JWTRefreshSecret: getEnv("FASTERP_JWT_REFRESH_SECRET", "change-me-refresh-secret"),
		ModulesDir:       getEnv("FASTERP_MODULES_DIR", "./modules"),
		UploadDir:        getEnv("FASTERP_UPLOAD_DIR", "./uploads"),
		StaticDir:        getEnv("FASTERP_STATIC_DIR", "./static"),
		TemplatesDir:     getEnv("FASTERP_TEMPLATES_DIR", "./templates"),
		CORSOrigins:      getEnv("FASTERP_CORS_ORIGINS", "http://localhost:5051"),
		RateLimit:        getEnvInt("FASTERP_RATE_LIMIT", 100),
		AuthRateLimit:    getEnvInt("FASTERP_AUTH_RATE_LIMIT", 10),
		MaxOpenConns:     getEnvInt("FASTERP_DB_MAX_OPEN", 50),
		MaxIdleConns:     getEnvInt("FASTERP_DB_MAX_IDLE", 10),
		Seed:             getEnvBool("FASTERP_SEED", true),
		Dev:              getEnvBool("FASTERP_DEV", false),
		TrustedProxies:   getEnvList("FASTERP_TRUSTED_PROXIES"),
	}
}

// ValidateSecretWeak reports whether a secret is unusable in production. It is
// deliberately not an error in dev: the defaults exist so that `go run` works
// out of the box, and the same defaults that make that convenient also let
// anyone who has read the README mint admin tokens.
func ValidateSecretWeak(secret string) bool {
	switch secret {
	case "", "change-me-in-production", "change-me-refresh-secret", "dev-secret", "dev-secret-refresh", "secret":
		return true
	}
	return len(secret) < 16
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("[Config] %s=%q is not a boolean, using %v", key, v, fallback)
		return fallback
	}
	return b
}

func getEnvList(key string) []string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
