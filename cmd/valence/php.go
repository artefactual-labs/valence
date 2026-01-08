package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dunglas/frankenphp"
)

func initPHPRuntime() error {
	if err := frankenphp.Init(frankenphp.WithPhpIni(defaultPHPIni())); err != nil {
		return err
	}
	if !frankenphp.Config().ZTS {
		frankenphp.Shutdown()
		return fmt.Errorf("frankenphp built without ZTS; rebuild PHP with --enable-zts to match Valence defaults")
	}
	return nil
}

func shutdownPHPRuntime() {
	frankenphp.Shutdown()
}

func defaultPHPIni() map[string]string {
	ini := map[string]string{
		"output_buffering":              "4096",
		"expose_php":                    "0",
		"log_errors":                    "1",
		"error_reporting":               "E_ALL",
		"display_errors":                "stderr",
		"display_startup_errors":        "1",
		"zend.max_allowed_stack_size":   "-1",
		"zend.reserved_stack_size":      "0",
		"max_execution_time":            "120",
		"max_input_time":                "120",
		"memory_limit":                  "512M",
		"post_max_size":                 "72M",
		"default_charset":               "UTF-8",
		"cgi.fix_pathinfo":              "0",
		"upload_max_filesize":           "64M",
		"max_file_uploads":              "20",
		"date.timezone":                 "America/Vancouver",
		"session.use_only_cookies":      "0",
		"opcache.fast_shutdown":         "1",
		"opcache.max_accelerated_files": "10000",
		"opcache.validate_timestamps":   "0",
	}

	if extDir := detectExtensionDir(); extDir != "" {
		ini["extension_dir"] = extDir
		log.Printf("php extension_dir=%s", extDir)
	}

	return ini
}

func detectExtensionDir() string {
	if dir := strings.TrimSpace(os.Getenv("PHP_EXTENSION_DIR")); dir != "" {
		if extDirHasSo(dir) {
			return dir
		}
	}

	for _, base := range []string{
		"/usr/local/lib/php/extensions",
		"/lib/php/extensions",
		"/usr/lib/php/extensions",
	} {
		if dir := firstExtDirWithSo(base); dir != "" {
			return dir
		}
	}

	return ""
}

func firstExtDirWithSo(base string) string {
	if extDirHasSo(base) {
		return base
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(base, entry.Name())
		if extDirHasSo(path) {
			return path
		}
	}
	return ""
}

func extDirHasSo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			return true
		}
	}
	return false
}

func runSymfonyPurge(root string) error {
	log.Printf("running symfony tools:purge --demo")
	return runSymfonyWithMemoryLimit(root, []string{"tools:purge", "--demo"}, "-1")
}

func runSymfonyCacheClear(root string) error {
	log.Printf("running symfony cc")
	return runSymfony(root, []string{"cc"})
}

func runSymfony(root string, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	script := filepath.Join(root, "symfony")
	argv := append([]string{script}, args...)
	exitCode := frankenphp.ExecuteScriptCLI(script, argv)
	if exitCode != 0 {
		return fmt.Errorf("symfony command failed with exit code %d", exitCode)
	}
	return nil
}

func runSymfonyWithMemoryLimit(root string, args []string, memoryLimit string) error {
	wrapper, err := writeSymfonyWrapper(root, args, memoryLimit)
	if err != nil {
		return err
	}
	defer os.Remove(wrapper)

	exitCode := frankenphp.ExecuteScriptCLI(wrapper, []string{wrapper})
	if exitCode != 0 {
		return fmt.Errorf("symfony command failed with exit code %d", exitCode)
	}
	return nil
}

func writeSymfonyWrapper(root string, args []string, memoryLimit string) (string, error) {
	script := filepath.Join(root, "symfony")
	argv := append([]string{script}, args...)

	tmp, err := os.CreateTemp("", "valence-symfony-*.php")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	php := strings.Builder{}
	php.WriteString("<?php\n")
	php.WriteString(fmt.Sprintf("ini_set('memory_limit', '%s');\n", phpEscape(memoryLimit)))
	php.WriteString(fmt.Sprintf("chdir('%s');\n", phpEscape(root)))
	php.WriteString("$argv = [\n")
	for _, arg := range argv {
		php.WriteString(fmt.Sprintf("  '%s',\n", phpEscape(arg)))
	}
	php.WriteString("];\n")
	php.WriteString("$argc = count($argv);\n")
	php.WriteString("$_SERVER['argv'] = $argv;\n")
	php.WriteString("$_SERVER['argc'] = $argc;\n")
	php.WriteString(fmt.Sprintf("require '%s';\n", phpEscape(script)))

	if _, err := tmp.WriteString(php.String()); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func phpEscape(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "'", "\\'")
}

type frontControllerHandler struct {
	phpRoot         string
	frontController string
}

func (h *frontControllerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Route the request through the legacy Symfony front controller.
	req, env := h.frontControllerRequest(r)
	phpReq, err := frankenphp.NewRequestWithContext(
		req,
		frankenphp.WithRequestDocumentRoot(h.phpRoot, false),
		frankenphp.WithRequestEnv(env),
	)
	if err != nil {
		log.Printf("php request build error for %s: %v", r.URL.Path, err)
		http.Error(w, "php request build error", http.StatusBadGateway)
		return
	}

	if err := frankenphp.ServeHTTP(w, phpReq); err != nil {
		var rejected *frankenphp.ErrRejected
		switch {
		case errors.As(err, &rejected):
			http.Error(w, "request rejected by PHP", http.StatusBadRequest)
		default:
			log.Printf("php error for %s: %v", r.URL.Path, err)
			http.Error(w, "php execution error", http.StatusBadGateway)
		}
	}
}

func (h *frontControllerHandler) frontControllerRequest(r *http.Request) (*http.Request, map[string]string) {
	originalPath := r.URL.Path

	clone := r.Clone(r.Context())
	clone.URL.Path = "/index.php"
	clone.URL.RawPath = "/index.php"

	env := map[string]string{
		"SCRIPT_FILENAME": h.frontController,
		"SCRIPT_NAME":     "/index.php",
		"PATH_INFO":       originalPath,
	}

	return clone, env
}
