package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Modules  ModulesConfig
	Logging  LoggingConfig
	Security SecurityConfig
}

type ServerConfig struct {
	Port string
	Host string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	URL      string // Computed
}

type ModulesConfig struct {
	Path string
}

type LoggingConfig struct {
	Level string
}

type SecurityConfig struct {
	JWTSecret        string
	JWTRefreshSecret string
}

var globalConfig *Config

func Load() *Config {
	if globalConfig != nil {
		return globalConfig
	}

	// Intentar cargar .env
	_ = godotenv.Load()

	cfg := &Config{
		Server: ServerConfig{
			Port: getEnv("FASTERP_PORT", "7071"),
			Host: getEnv("FASTERP_HOST", "0.0.0.0"),
		},
		Database: DatabaseConfig{
			Host:     getEnv("FASTERP_DB_HOST", "localhost"),
			Port:     getEnv("FASTERP_DB_PORT", "5432"),
			User:     getEnv("FASTERP_DB_USER", "postgres"),
			Password: getEnv("FASTERP_DB_PASSWORD", "postgres"),
			Name:     getEnv("FASTERP_DB_NAME", "fasterp"),
		},
		Modules: ModulesConfig{
			Path: getEnv("FASTERP_MODULES_PATH", "../modules"),
		},
		Logging: LoggingConfig{
			Level: getEnv("FASTERP_LOG_LEVEL", "info"),
		},
		Security: SecurityConfig{
			JWTSecret:        getEnv("FASTERP_JWT_SECRET", "change-me-in-production"),
			JWTRefreshSecret: getEnv("FASTERP_JWT_REFRESH_SECRET", "change-me-in-production-refresh"),
		},
	}

	// Intentar cargar fast.conf
	if _, err := os.Stat("fast.conf"); err == nil {
		log.Println("[Config] Loading fast.conf...")
		if err := loadConfFile("fast.conf", cfg); err != nil {
			log.Printf("[WARN] Failed to load fast.conf: %v", err)
		}
	}

	// Computar DB URL
	cfg.Database.URL = fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.Name,
	)

	log.Printf("[Config] Loaded: server=%s:%s db=%s@%s:%s",
		cfg.Server.Host, cfg.Server.Port,
		cfg.Database.User, cfg.Database.Host, cfg.Database.Port)

	globalConfig = cfg
	return cfg
}

func loadConfFile(filename string, cfg *Config) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var section string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse sections [name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimPrefix(strings.TrimSuffix(line, "]"), "[")
			continue
		}

		// Parse key = value
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			applyConfig(section, key, val, cfg)
		}
	}

	return nil
}

func applyConfig(section, key, val string, cfg *Config) {
	switch section {
	case "server":
		switch key {
		case "port":
			cfg.Server.Port = val
		case "host":
			cfg.Server.Host = val
		}
	case "database":
		switch key {
		case "host":
			cfg.Database.Host = val
		case "port":
			cfg.Database.Port = val
		case "user":
			cfg.Database.User = val
		case "password":
			cfg.Database.Password = val
		case "name":
			cfg.Database.Name = val
		}
	case "modules":
		switch key {
		case "path":
			cfg.Modules.Path = val
		}
	case "logging":
		switch key {
		case "level":
			cfg.Logging.Level = val
		}
	case "security":
		switch key {
		case "jwt_secret":
			cfg.Security.JWTSecret = val
		case "jwt_refresh_secret":
			cfg.Security.JWTRefreshSecret = val
		}
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func GetEnvBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}
