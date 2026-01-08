package bootstrap

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	AtomDir     string
	AtomDataDir string

	DevelopmentMode bool

	ElasticsearchHost string
	MemcachedHost     string
	GearmandHost      string
	MySQLDSN          string
	MySQLUsername     string
	MySQLPassword     string
	DebugIP           string
}

type Summary struct {
	Written []string
	Skipped []string
}

func (c Config) dataDir() string {
	if strings.TrimSpace(c.AtomDataDir) != "" {
		return c.AtomDataDir
	}
	return c.AtomDir
}

func (c Config) appConfigDir() string {
	return filepath.Join(c.dataDir(), "apps/qubit/config")
}

func (c Config) projectConfigDir() string {
	return filepath.Join(c.dataDir(), "config")
}

func (c Config) sourceAppConfigDir() string {
	return filepath.Join(c.AtomDir, "apps/qubit/config")
}

func (c Config) sourceProjectConfigDir() string {
	return filepath.Join(c.AtomDir, "config")
}

func LoadConfigFromEnv(atomDir string) (Config, error) {
	cfg := Config{
		AtomDir:           atomDir,
		AtomDataDir:       envOrDefault("ATOM_DATA_DIR", ""),
		DevelopmentMode:   envBool("ATOM_DEVELOPMENT_MODE", false),
		ElasticsearchHost: mustEnv("ATOM_ELASTICSEARCH_HOST"),
		MemcachedHost:     mustEnv("ATOM_MEMCACHED_HOST"),
		GearmandHost:      mustEnv("ATOM_GEARMAND_HOST"),
		MySQLDSN:          mustEnv("ATOM_MYSQL_DSN"),
		MySQLUsername:     mustEnv("ATOM_MYSQL_USERNAME"),
		MySQLPassword:     mustEnv("ATOM_MYSQL_PASSWORD"),
		DebugIP:           envOrDefault("ATOM_DEBUG_IP", ""),
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	var missing []string
	if c.ElasticsearchHost == "" {
		missing = append(missing, "ATOM_ELASTICSEARCH_HOST")
	}
	if c.MemcachedHost == "" {
		missing = append(missing, "ATOM_MEMCACHED_HOST")
	}
	if c.GearmandHost == "" {
		missing = append(missing, "ATOM_GEARMAND_HOST")
	}
	if c.MySQLDSN == "" {
		missing = append(missing, "ATOM_MYSQL_DSN")
	}
	if c.MySQLUsername == "" {
		missing = append(missing, "ATOM_MYSQL_USERNAME")
	}
	if c.MySQLPassword == "" {
		missing = append(missing, "ATOM_MYSQL_PASSWORD")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func Apply(cfg Config) (Summary, error) {
	var summary Summary

	if err := ensureDir(cfg.appConfigDir()); err != nil {
		return summary, err
	}
	if err := ensureDir(cfg.projectConfigDir()); err != nil {
		return summary, err
	}
	if err := syncConfigDir(&summary, cfg); err != nil {
		return summary, err
	}

	// /apps/qubit/config/settings.yml
	if err := writeSettingsYML(&summary, cfg); err != nil {
		return summary, err
	}

	// /config/propel.ini (always overwrite)
	if err := overwriteFromTemplate(&summary,
		filepath.Join(cfg.projectConfigDir(), "propel.ini"),
		filepath.Join(cfg.sourceProjectConfigDir(), "propel.ini.tmpl"),
	); err != nil {
		return summary, err
	}

	// /config/databases.yml (always overwrite)
	if err := overwriteFile(&summary, filepath.Join(cfg.projectConfigDir(), "databases.yml"), buildDatabasesYML(cfg)); err != nil {
		return summary, err
	}

	// /config/appChallenge.yml
	if err := copyIfMissing(&summary,
		filepath.Join(cfg.projectConfigDir(), "appChallenge.yml"),
		filepath.Join(cfg.sourceProjectConfigDir(), "appChallenge.yml.tmpl"),
	); err != nil {
		return summary, err
	}

	// /config/ProjectConfiguration.class.php (shim for data dir)
	if err := writeProjectConfigurationShim(&summary, cfg); err != nil {
		return summary, err
	}

	// /apps/qubit/config/gearman.yml (always overwrite)
	gearmanYML := fmt.Sprintf("all:\n  servers:\n    default: %s\n", cfg.GearmandHost)
	if err := overwriteFile(&summary, filepath.Join(cfg.appConfigDir(), "gearman.yml"), gearmanYML); err != nil {
		return summary, err
	}

	// /apps/qubit/config/app.yml
	if err := writeAppYMLIfMissing(&summary, cfg); err != nil {
		return summary, err
	}

	// /apps/qubit/config/factories.yml
	if err := writeFactoriesYMLIfMissing(&summary, cfg); err != nil {
		return summary, err
	}

	// /config/search.yml (always overwrite)
	searchYML := buildSearchYML(cfg)
	if err := overwriteFile(&summary, filepath.Join(cfg.projectConfigDir(), "search.yml"), searchYML); err != nil {
		return summary, err
	}

	// /config/config.php (always overwrite)
	if err := overwriteFile(&summary, filepath.Join(cfg.projectConfigDir(), "config.php"), buildConfigPHP(cfg)); err != nil {
		return summary, err
	}

	// php ini (conf.d drop-in)
	// sf symlink
	if err := ensureSFSymlink(&summary, cfg); err != nil {
		return summary, err
	}

	return summary, nil
}

func writeSettingsYML(summary *Summary, cfg Config) error {
	target := filepath.Join(cfg.appConfigDir(), "settings.yml")
	source := target
	if !exists(target) {
		source = filepath.Join(cfg.sourceAppConfigDir(), "settings.yml.tmpl")
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	secret, err := randomHex(16)
	if err != nil {
		return err
	}
	updated := string(content)
	updated = strings.ReplaceAll(updated, "change_me", secret)
	updated = strings.ReplaceAll(updated, "no_script_name:         false", "no_script_name:         true")
	return overwriteFile(summary, target, updated)
}

func writeAppYMLIfMissing(summary *Summary, cfg Config) error {
	target := filepath.Join(cfg.appConfigDir(), "app.yml")
	if exists(target) {
		summary.Skipped = append(summary.Skipped, target)
		return nil
	}
	host, port := splitHostPort(cfg.MemcachedHost, 11211)
	appYML := fmt.Sprintf("all:\n  upload_limit: -1\n  download_timeout: 10\n  cache_engine: sfMemcacheCache\n  cache_engine_param:\n    host: %s\n    port: %d\n    prefix: atom\n    storeCacheInfo: true\n    persistent: true\n  read_only: false\n  htmlpurifier_enabled: false\n  csp:\n    response_header: Content-Security-Policy\n    directives: >\n      default-src 'self';\n      font-src 'self' https://fonts.gstatic.com;\n      form-action 'self';\n      img-src 'self' https://*.googleapis.com https://*.gstatic.com *.google.com  *.googleusercontent.com data: https://www.gravatar.com/avatar/ https://*.google-analytics.com https://*.googletagmanager.com blob:;\n      script-src 'self' https://*.googletagmanager.com 'nonce' https://*.googleapis.com https://*.gstatic.com *.google.com https://*.ggpht.com *.googleusercontent.com blob:;\n      style-src 'self' 'nonce' https://fonts.googleapis.com;\n      worker-src 'self' blob:;\n      connect-src 'self' https://*.google-analytics.com https://*.analytics.google.com https://*.googletagmanager.com https://*.googleapis.com *.google.com https://*.gstatic.com  data: blob:;\n      frame-ancestors 'self';\n\n", host, port)
	return writeFile(summary, target, appYML)
}

func writeFactoriesYMLIfMissing(summary *Summary, cfg Config) error {
	target := filepath.Join(cfg.appConfigDir(), "factories.yml")
	if exists(target) {
		summary.Skipped = append(summary.Skipped, target)
		return nil
	}
	host, port := splitHostPort(cfg.MemcachedHost, 11211)
	secureCookie := "true"
	if cfg.DevelopmentMode {
		secureCookie = "false"
	}

	factories := fmt.Sprintf("prod:\n  storage:\n    class: QubitCacheSessionStorage\n    param:\n      session_name: symfony\n      session_cookie_httponly: true\n      session_cookie_secure: %s\n      cache:\n        class: sfMemcacheCache\n        param:\n          host: %s\n          port: %d\n          prefix: atom\n          storeCacheInfo: true\n          persistent: true\n\n\n", secureCookie, host, port)
	factories += fmt.Sprintf("dev:\n  storage:\n    class: QubitCacheSessionStorage\n    param:\n      session_name: symfony\n      session_cookie_httponly: true\n      session_cookie_secure: %s\n      cache:\n        class: sfMemcacheCache\n        param:\n          host: %s\n          port: %d\n          prefix: atom\n          storeCacheInfo: true\n          persistent: true\n\n", secureCookie, host, port)
	return writeFile(summary, target, factories)
}

func buildDatabasesYML(cfg Config) string {
	return fmt.Sprintf("dev:\n  propel:\n    param:\n      classname: PropelPDO\n      debug:\n        realmemoryusage: true\n        details:\n          time: { enabled: true }\n          slow: { enabled: true, threshold: 0.1 }\n          mem: { enabled: true }\n          mempeak: { enabled: true }\n          memdelta: { enabled: true }\n\ntest:\n  propel:\n    param:\n      classname: PropelPDO\n\nall:\n  propel:\n    class: sfPropelDatabase\n    param:\n      classname: PropelPDO\n      dsn: %s\n      username: %s\n      password: %s\n      encoding: utf8mb4\n      persistent: true\n      pooling: true\n", cfg.MySQLDSN, cfg.MySQLUsername, cfg.MySQLPassword)
}

func buildSearchYML(cfg Config) string {
	host, port := splitHostPort(cfg.ElasticsearchHost, 9200)
	return fmt.Sprintf("all:\n  server:\n    host: %s\n    port: %d\n\n", host, port)
}

func buildConfigPHP(cfg Config) string {
	return fmt.Sprintf("<?php\n\nreturn [\n    'all' => [\n        'propel' => [\n            'class' => 'sfPropelDatabase',\n            'param' => [\n                'encoding' => 'utf8mb4',\n                'persistent' => true,\n                'pooling' => true,\n                'dsn' => '%s',\n                'username' => '%s',\n                'password' => '%s',\n            ],\n        ],\n    ],\n    'dev' => [\n        'propel' => [\n            'param' => [\n                'classname' => 'PropelPDO',\n                'debug' => [\n                    'realmemoryusage' => true,\n                    'details' => [\n                        'time' => [\n                            'enabled' => true,\n                        ],\n                        'slow' => [\n                            'enabled' => true,\n                            'threshold' => 0.1,\n                        ],\n                        'mem' => [\n                            'enabled' => true,\n                        ],\n                        'mempeak' => [\n                            'enabled' => true,\n                        ],\n                        'memdelta' => [\n                            'enabled' => true,\n                        ],\n                    ],\n                ],\n            ],\n        ],\n    ],\n    'test' => [\n        'propel' => [\n            'param' => [\n                'classname' => 'PropelPDO',\n            ],\n        ],\n    ],\n];\n", cfg.MySQLDSN, cfg.MySQLUsername, cfg.MySQLPassword)
}

func ensureSFSymlink(summary *Summary, cfg Config) error {
	target := filepath.Join(cfg.AtomDir, "vendor/symfony/data/web/sf")
	link := filepath.Join(cfg.AtomDir, "sf")

	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("expected %s to be symlink", link)
		}
		current, err := os.Readlink(link)
		if err != nil {
			return err
		}
		if current == target {
			summary.Skipped = append(summary.Skipped, link)
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.Symlink(target, link); err != nil {
		return err
	}
	summary.Written = append(summary.Written, link)
	return nil
}

func overwriteFromTemplate(summary *Summary, target, tmpl string) error {
	if err := copyFile(tmpl, target); err != nil {
		return err
	}
	summary.Written = append(summary.Written, target)
	return nil
}

func writeProjectConfigurationShim(summary *Summary, cfg Config) error {
	if cfg.dataDir() == cfg.AtomDir {
		return nil
	}

	target := filepath.Join(cfg.projectConfigDir(), "ProjectConfiguration.class.php")
	source := filepath.Join(cfg.AtomDir, "config", "ProjectConfiguration.class.php")
	contents := fmt.Sprintf("<?php\n// Auto-generated by Valence; delegate to the app's config.\nrequire_once '%s';\n", phpPathEscape(source))
	return overwriteFile(summary, target, contents)
}

func syncConfigDir(summary *Summary, cfg Config) error {
	if cfg.dataDir() == cfg.AtomDir {
		return nil
	}

	skip := map[string]bool{
		"ProjectConfiguration.class.php": true,
		"app.yml":                        true,
		"factories.yml":                  true,
	}
	sourceRoot := cfg.sourceProjectConfigDir()
	targetRoot := cfg.projectConfigDir()

	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip[rel] || strings.HasSuffix(rel, ".tmpl") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		target := filepath.Join(targetRoot, rel)
		if entry.IsDir() {
			return ensureDir(target)
		}
		if exists(target) {
			return nil
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		summary.Written = append(summary.Written, target)
		return nil
	})
}

func copyIfMissing(summary *Summary, target, tmpl string) error {
	if exists(target) {
		summary.Skipped = append(summary.Skipped, target)
		return nil
	}
	if err := copyFile(tmpl, target); err != nil {
		return err
	}
	summary.Written = append(summary.Written, target)
	return nil
}

func overwriteFile(summary *Summary, target, contents string) error {
	if err := writeFile(summary, target, contents); err != nil {
		return err
	}
	summary.Written = append(summary.Written, target)
	return nil
}

func writeFile(summary *Summary, target, contents string) error {
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(contents), 0644)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := ensureDir(filepath.Dir(dest)); err != nil {
		return err
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func phpPathEscape(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "'", "\\'")
}

func splitHostPort(value string, defaultPort int) (string, int) {
	if value == "" {
		return "", defaultPort
	}
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		return parts[0], defaultPort
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return parts[0], defaultPort
	}
	return parts[0], port
}

func envOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

func mustEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envBool(key string, def bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return def
	}
	return parsed
}
