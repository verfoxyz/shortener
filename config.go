package main

import (
	"os"
)

type Config struct {
	Addr    string
	DBPath  string
	BaseURL string
}

func LoadConfig() Config {
	cfg := Config{
		Addr:    getEnv("ADDR", ":8080"),
		DBPath:  getEnv("DB_PATH", "shortener.db"),
		BaseURL: getEnv("BASE_URL", "http://localhost:8080"),
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
