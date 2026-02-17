package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	NATS     NATSConfig     `yaml:"nats"`
	MinIO    MinIOConfig    `yaml:"minio"`
	Vision   VisionConfig   `yaml:"vision"`
	Tracking TrackingConfig `yaml:"tracking"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Port   int    `yaml:"port"`
	APIKey string `yaml:"api_key"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	MaxConns int    `yaml:"max_conns"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

type NATSConfig struct {
	URL string `yaml:"url"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type VisionConfig struct {
	ModelsDir            string  `yaml:"models_dir"`
	DetectionThreshold   float64 `yaml:"detection_threshold"`
	RecognitionThreshold float64 `yaml:"recognition_threshold"`
	DefaultFPS           int     `yaml:"default_fps"`
	MaxFPS               int     `yaml:"max_fps"`
	WorkerCount          int     `yaml:"worker_count"`
	FrameWidth           int     `yaml:"frame_width"`
}

type TrackingConfig struct {
	MaxAge              int           `yaml:"max_age"`
	MinHits             int           `yaml:"min_hits"`
	ReRecognizeInterval time.Duration `yaml:"re_recognize_interval"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Load reads config from YAML file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyEnvOverrides(cfg)
	setDefaults(cfg)

	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.MaxConns == 0 {
		cfg.Database.MaxConns = 20
	}
	if cfg.Vision.DefaultFPS == 0 {
		cfg.Vision.DefaultFPS = 5
	}
	if cfg.Vision.MaxFPS == 0 {
		cfg.Vision.MaxFPS = 10
	}
	if cfg.Vision.WorkerCount == 0 {
		cfg.Vision.WorkerCount = 6
	}
	if cfg.Vision.FrameWidth == 0 {
		cfg.Vision.FrameWidth = 640
	}
	if cfg.Vision.DetectionThreshold == 0 {
		cfg.Vision.DetectionThreshold = 0.5
	}
	if cfg.Vision.RecognitionThreshold == 0 {
		cfg.Vision.RecognitionThreshold = 0.4
	}
	if cfg.Tracking.MaxAge == 0 {
		cfg.Tracking.MaxAge = 30
	}
	if cfg.Tracking.MinHits == 0 {
		cfg.Tracking.MinHits = 3
	}
	if cfg.Tracking.ReRecognizeInterval == 0 {
		cfg.Tracking.ReRecognizeInterval = 3 * time.Second
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("FD_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("FD_API_KEY"); v != "" {
		cfg.Server.APIKey = v
	}
	if v := os.Getenv("FD_DB_HOST"); v != "" {
		cfg.Database.Host = v
	}
	if v := os.Getenv("FD_DB_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Database.Port = port
		}
	}
	if v := os.Getenv("FD_DB_NAME"); v != "" {
		cfg.Database.Name = v
	}
	if v := os.Getenv("FD_DB_USER"); v != "" {
		cfg.Database.User = v
	}
	if v := os.Getenv("FD_DB_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("FD_NATS_URL"); v != "" {
		cfg.NATS.URL = v
	}
	if v := os.Getenv("FD_MINIO_ENDPOINT"); v != "" {
		cfg.MinIO.Endpoint = v
	}
	if v := os.Getenv("FD_MINIO_ACCESS_KEY"); v != "" {
		cfg.MinIO.AccessKey = v
	}
	if v := os.Getenv("FD_MINIO_SECRET_KEY"); v != "" {
		cfg.MinIO.SecretKey = v
	}
	if v := os.Getenv("FD_MINIO_BUCKET"); v != "" {
		cfg.MinIO.Bucket = v
	}
	if v := os.Getenv("FD_MODELS_DIR"); v != "" {
		cfg.Vision.ModelsDir = v
	}
	if v := os.Getenv("FD_VISION_WORKER_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Vision.WorkerCount = n
		}
	}
}
