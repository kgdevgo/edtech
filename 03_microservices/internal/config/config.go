package config

import "os"

type Config struct {
	App   AppConfig
	DB    DBConfig
	Kafka KafkaConfig
	Redis RedisConfig
	SMTP  SMTPConfig
}

type AppConfig struct {
	Port      string
	JWTSecret string
}

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

type KafkaConfig struct {
	Broker string
	Topic  string
}

type RedisConfig struct {
	Addr string
}

type SMTPConfig struct {
	Host     string
	Port     string
	User     string
	Password string
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func Load() *Config {
	return &Config{
		App: AppConfig{
			Port:      getEnv("PORT", "8080"),
			JWTSecret: getEnv("JWT_SECRET", ""),
		},
		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "secret"),
			Name:     getEnv("DB_NAME", "edtech"),
		},
		Kafka: KafkaConfig{
			Broker: getEnv("KAFKA_BROKER", "localhost:29092"),
			Topic:  getEnv("KAFKA_TOPIC", "enrollments"),
		},
		Redis: RedisConfig{
			Addr: getEnv("REDIS_ADDR", "localhost:6379"),
		},
		SMTP: SMTPConfig{
			Host:     getEnv("SMTP_HOST", "smtp.gmail.com"),
			Port:     getEnv("SMTP_PORT", "587"),
			User:     getEnv("SMTP_USER", ""),
			Password: getEnv("SMTP_PASSWORD", ""),
		},
	}
}
