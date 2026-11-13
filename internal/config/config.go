package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port        string
	Environment string

	// Database (Neon Serverless PostgreSQL 16)
	DatabaseURL          string
	DBMaxConns          int32
	DBMinConns          int32
	DBMaxConnLifetime   time.Duration
	DBMaxConnIdleTime   time.Duration

	// Redis Cloud
	RedisURL      string
	RedisPassword string

	// Paystack Clearing Rail
	PaystackSecretKey    string
	PaystackPublicKey    string
	PaystackBaseURL      string
	PaystackBypassIPCheck bool
	MockPaystackSuccess   bool

	// Security & Anti-Replay
	ReplayWindowSec       int64

	// Africa's Talking SMS Gateway
	AfricaTalkingUsername string
	AfricaTalkingAPIKey   string
	AfricaTalkingSenderID string
	AfricaTalkingSandbox  bool
	MockSMS               bool
}

func Load() *Config {
	// Try loading .env, but ignore error if not found in production
	_ = godotenv.Load()

	cfg := &Config{
		Port:                  getEnv("PORT", "8080"),
		Environment:           getEnv("ENV", "development"),
		DatabaseURL:           getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/mtap?sslmode=disable"),
		DBMaxConns:            int32(getEnvAsInt("DB_MAX_CONNS", 25)),
		DBMinConns:            int32(getEnvAsInt("DB_MIN_CONNS", 5)),
		DBMaxConnLifetime:     getEnvAsDuration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		DBMaxConnIdleTime:     getEnvAsDuration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
		RedisURL:              getEnv("REDIS_URL", "localhost:6379"),
		RedisPassword:         getEnv("REDIS_PASSWORD", ""),
		PaystackSecretKey:     getEnv("PAYSTACK_SECRET_KEY", "sk_test_mock_secret_key"),
		PaystackPublicKey:     getEnv("PAYSTACK_PUBLIC_KEY", "pk_test_mock_public_key"),
		PaystackBaseURL:       getEnv("PAYSTACK_BASE_URL", "https://api.paystack.co"),
		PaystackBypassIPCheck: getEnvAsBool("PAYSTACK_BYPASS_IP_CHECK", true), // Default true in dev for convenience
		MockPaystackSuccess:   getEnvAsBool("MOCK_PAYSTACK_SUCCESS", true),
		ReplayWindowSec:       int64(getEnvAsInt("REPLAY_WINDOW_SEC", 300)), // Default 300s (5m) to allow Swagger testing and Render cold-starts
		AfricaTalkingUsername: getEnv("AFRICASTALKING_USERNAME", "sandbox"),
		AfricaTalkingAPIKey:   getEnv("AFRICASTALKING_API_KEY", "mock_at_api_key"),
		AfricaTalkingSenderID: getEnv("AFRICASTALKING_SENDER_ID", ""),
		AfricaTalkingSandbox:  getEnvAsBool("AFRICASTALKING_SANDBOX", true),
		MockSMS:               getEnvAsBool("MOCK_SMS", true),
	}

	slog.Info("Configuration loaded",
		"env", cfg.Environment,
		"port", cfg.Port,
		"mock_paystack", cfg.MockPaystackSuccess,
		"mock_sms", cfg.MockSMS,
		"at_sandbox", cfg.AfricaTalkingSandbox,
	)

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvAsBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}
	return defaultVal
}

func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if dur, err := time.ParseDuration(val); err == nil {
			return dur
		}
	}
	return defaultVal
}

