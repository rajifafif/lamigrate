package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
)

const maxConfigFileSize = 1 << 20

var defaultConfigNames = []string{"config.yaml", "config.yml", ".env"}

type dbMySQLConfig struct {
	Host    string `yaml:"host"`
	Timeout string `yaml:"timeout"`
	Port    int    `yaml:"port"`
	User    string `yaml:"user"`
	Pass    string `yaml:"pass"`
	DBName  string `yaml:"dbName"`
}

type yamlConfig struct {
	DBMySQL dbMySQLConfig `yaml:"dbMySQL"`
}

// resolveDSN applies this precedence for database commands:
// explicit -dsn, LAMIGRATE_DSN, explicit -config, then config.yaml,
// config.yml, or .env in the current project directory.
func resolveDSN(explicitDSN, configPath, searchDir string) (string, error) {
	if dsn := strings.TrimSpace(explicitDSN); dsn != "" {
		return dsn, nil
	}
	if dsn := strings.TrimSpace(os.Getenv("LAMIGRATE_DSN")); dsn != "" {
		return dsn, nil
	}

	path := strings.TrimSpace(configPath)
	if path == "" {
		var err error
		path, err = findDefaultConfig(searchDir)
		if err != nil {
			return "", err
		}
	}
	return dsnFromConfigFile(path)
}

func findDefaultConfig(searchDir string) (string, error) {
	for _, name := range defaultConfigNames {
		path := filepath.Join(searchDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("lamigrate: inspect config file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("lamigrate: config path %s must be a regular file", path)
		}
		return path, nil
	}
	return "", fmt.Errorf("lamigrate: database configuration not found; provide -dsn, LAMIGRATE_DSN, -config, config.yaml/config.yml, or .env")
}

func dsnFromConfigFile(path string) (string, error) {
	data, err := readConfigFile(path)
	if err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(path))
	var config dbMySQLConfig
	switch ext {
	case ".yaml", ".yml":
		var parsed yamlConfig
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&parsed); err != nil {
			return "", fmt.Errorf("lamigrate: parse YAML config %s: %w", path, err)
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return "", fmt.Errorf("lamigrate: parse YAML config %s: multiple documents are not supported", path)
			}
			return "", fmt.Errorf("lamigrate: parse YAML config %s: %w", path, err)
		}
		config = parsed.DBMySQL
	case ".env":
		values, err := parseDotEnv(data)
		if err != nil {
			return "", fmt.Errorf("lamigrate: parse .env %s: %w", path, err)
		}
		if dsn := strings.TrimSpace(values["LAMIGRATE_DSN"]); dsn != "" {
			return dsn, nil
		}
		config = dbConfigFromEnv(values)
	default:
		return "", fmt.Errorf("lamigrate: unsupported config file %s; use .env, .yaml, or .yml", path)
	}

	return formatMySQLDSN(config, path)
}

func readConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: read config file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lamigrate: config path %s must be a regular file", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: open config file %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxConfigFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("lamigrate: read config file %s: %w", path, err)
	}
	if len(data) > maxConfigFileSize {
		return nil, fmt.Errorf("lamigrate: config file %s exceeds %d bytes", path, maxConfigFileSize)
	}
	return data, nil
}

func dbConfigFromEnv(values map[string]string) dbMySQLConfig {
	return dbMySQLConfig{
		Host:    firstEnvValue(values, "LAMIGRATE_DB_HOST", "DB_MYSQL_HOST", "DB_HOST"),
		Timeout: firstEnvValue(values, "LAMIGRATE_DB_TIMEOUT", "DB_MYSQL_TIMEOUT", "DB_TIMEOUT"),
		Port:    parseEnvPort(firstEnvValue(values, "LAMIGRATE_DB_PORT", "DB_MYSQL_PORT", "DB_PORT")),
		User:    firstEnvValue(values, "LAMIGRATE_DB_USER", "DB_MYSQL_USER", "DB_USER"),
		Pass:    firstEnvValue(values, "LAMIGRATE_DB_PASS", "DB_MYSQL_PASS", "DB_PASS", "DB_PASSWORD"),
		DBName:  firstEnvValue(values, "LAMIGRATE_DB_NAME", "DB_MYSQL_DB_NAME", "DB_NAME", "DB_DATABASE"),
	}
}

func firstEnvValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseEnvPort(value string) int {
	port, _ := strconv.Atoi(strings.TrimSpace(value))
	return port
}

func formatMySQLDSN(config dbMySQLConfig, source string) (string, error) {
	if strings.TrimSpace(config.Host) == "" {
		return "", fmt.Errorf("lamigrate: dbMySQL.host is required in %s", source)
	}
	if strings.TrimSpace(config.User) == "" {
		return "", fmt.Errorf("lamigrate: dbMySQL.user is required in %s", source)
	}
	if strings.TrimSpace(config.DBName) == "" {
		return "", fmt.Errorf("lamigrate: dbMySQL.dbName is required in %s", source)
	}
	if config.Port < 1 || config.Port > 65535 {
		return "", fmt.Errorf("lamigrate: dbMySQL.port must be between 1 and 65535 in %s", source)
	}

	timeout := 30 * time.Second
	if raw := strings.TrimSpace(config.Timeout); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return "", fmt.Errorf("lamigrate: dbMySQL.timeout must be a positive Go duration in %s", source)
		}
		timeout = parsed
	}

	cfg := mysql.NewConfig()
	cfg.User = config.User
	cfg.Passwd = config.Pass
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	cfg.DBName = config.DBName
	cfg.ParseTime = true
	cfg.MultiStatements = true
	cfg.Timeout = timeout
	cfg.ReadTimeout = timeout
	cfg.WriteTimeout = timeout
	return cfg.FormatDSN(), nil
}

func parseDotEnv(data []byte) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), maxConfigFileSize)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "export ") {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "export "))
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=value", line)
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", line, key)
		}
		parsed, err := parseEnvValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		values[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, char := range key {
		if char == '_' || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (i > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	if value[0] == '"' {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value")
		}
		return parsed, nil
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	return value, nil
}
