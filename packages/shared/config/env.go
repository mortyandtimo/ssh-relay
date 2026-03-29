package config

import (
	"os"
	"strconv"
	"time"
)

func GetEnv(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func GetIntEnv(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func GetDurationEnvSeconds(key string, fallback int) time.Duration {
	return time.Duration(GetIntEnv(key, fallback)) * time.Second
}

