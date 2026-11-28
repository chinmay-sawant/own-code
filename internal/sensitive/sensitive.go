// Package sensitive classifies workspace-relative paths that must not be
// exposed to a model (agent context, read/list/search tools). It has no
// internal dependencies so both internal/app and internal/agenttools can use it.
package sensitive

import (
	"path/filepath"
	"strings"
)

// Path reports whether a slash- or OS-separated relative path names a file or
// directory that likely holds secrets.
func Path(relativePath string) bool {
	clean := strings.ToLower(filepath.FromSlash(strings.ReplaceAll(relativePath, `\`, "/")))
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if Name(part) {
			return true
		}
	}
	return false
}

// Name reports whether a single path component is sensitive.
func Name(part string) bool {
	part = strings.ToLower(part)
	if part == ".env" || strings.HasPrefix(part, ".env.") ||
		strings.Contains(part, "credential") || strings.Contains(part, "secret") ||
		strings.Contains(part, "password") || strings.Contains(part, "token") {
		return true
	}
	switch part {
	case "id_rsa", "id_dsa", "id_ed25519", "id_ecdsa", "known_hosts", "authorized_keys",
		".npmrc", ".netrc", ".pgpass", ".git-credentials", ".pypirc", ".htpasswd", ".dockercfg":
		return true
	}
	switch strings.ToLower(filepath.Ext(part)) {
	case ".key", ".pem", ".p12", ".pfx", ".keystore", ".jks", ".ppk":
		return true
	}
	return false
}
