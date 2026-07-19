package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port             int
	DatabaseURL      string
	JWTSecret        string
	JWTRefreshSecret string
	ModulesDir       string
	UploadDir        string
	CORSOrigins      string
	RateLimit        int
	AuthRateLimit    int
	MaxOpenConns     int
	MaxIdleConns     int
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
		CORSOrigins:      getEnv("FASTERP_CORS_ORIGINS", "http://localhost:5051"),
		RateLimit:        getEnvInt("FASTERP_RATE_LIMIT", 100),
		AuthRateLimit:    getEnvInt("FASTERP_AUTH_RATE_LIMIT", 10),
		MaxOpenConns:     getEnvInt("FASTERP_DB_MAX_OPEN", 50),
		MaxIdleConns:     getEnvInt("FASTERP_DB_MAX_IDLE", 10),
	}
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
