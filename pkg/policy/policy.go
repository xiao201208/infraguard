// Package policy manages policy library download and discovery.
package policy

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aliyun/infraguard/pkg/i18n"
	"github.com/aliyun/infraguard/pkg/models"
	"github.com/hashicorp/go-getter"
)

const (
	// DefaultRepo is empty so policy update defaults to the OSS policy archive.
	DefaultRepo = ""
	// DefaultPolicyVersion reads the latest policy version from the OSS version file.
	DefaultPolicyVersion = "latest"
	// DefaultPolicyGitRef is used when --repo is set without an explicit version.
	DefaultPolicyGitRef      = "main"
	defaultPolicyBaseURL     = "https://ros-public-tools.oss-cn-beijing.aliyuncs.com/github-releases/aliyun/infraguard/"
	defaultPolicyGitRepo     = "github.com/aliyun/infraguard"
	policyBaseURLEnv         = "INFRAGUARD_POLICY_BASE_URL"
	policyGitRepoEnv         = "INFRAGUARD_POLICY_GITHUB_REPO"
	policyDownloadTimeout    = 5 * time.Minute
	maxPolicyArchiveEntry    = 20 << 20
	maxPolicyArchiveTotal    = 500 << 20
	policyArchiveNamePattern = "infraguard-policies-%s.tar.gz"
)

// DefaultPolicyDir returns the default user-level policy storage directory (~/.infraguard/policies).
func DefaultPolicyDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".infraguard/policies"
	}
	// Allow override via environment variable for testing
	if dir := os.Getenv("INFRAGUARD_POLICY_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".infraguard", "policies")
}

// WorkspacePolicyDir returns the workspace-local policy directory (.infraguard/policies)
// relative to the current working directory.
func WorkspacePolicyDir() string {
	// Allow override via environment variable for testing
	if dir := os.Getenv("INFRAGUARD_WORKSPACE_POLICY_DIR"); dir != "" {
		return dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ".infraguard/policies"
	}
	return filepath.Join(cwd, ".infraguard", "policies")
}

// Loader handles policy loading with priority and indexing.
type Loader struct {
	policyDir    string
	index        *models.PolicyIndex
	extraModules []RegoModule // Extra helper modules for parsing rules
}

// Load discovers and loads all rules and packs from the policy directory.
// Supports two directory structures:
//  1. Provider-first: {provider}/rules/, {provider}/packs/, {provider}/lib/
//  2. Flat: {name}/*.rego (rules directly in subdirectory)
func (l *Loader) Load() error {
	// Iterate through subdirectories
	msg := i18n.Msg()
	entries, err := os.ReadDir(l.policyDir)
	if err != nil {
		return fmt.Errorf(msg.Errors.ReadPolicyDirectory, err)
	}

	// Initialize LibModules if not already
	if l.index.LibModules == nil {
		l.index.LibModules = make(map[string]string)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subDir := entry.Name()
		subDirPath := filepath.Join(l.policyDir, subDir)

		// Check if it's provider-first structure: {provider}/rules/ or {provider}/packs/
		rulesDir := filepath.Join(subDirPath, "rules")
		packsDir := filepath.Join(subDirPath, "packs")
		libDir := filepath.Join(subDirPath, "lib")
		hasRulesDir := dirExists(rulesDir)
		hasPacksDir := dirExists(packsDir)
		hasLibDir := dirExists(libDir)

		if hasRulesDir || hasPacksDir {
			// Provider-first structure: load from {provider}/rules/ and {provider}/packs/

			// Load lib modules first
			if hasLibDir {
				l.loadLibModulesFromDir(libDir)
			}

			if hasRulesDir {
				rules, err := DiscoverRulesWithExtraModules(rulesDir, l.extraModules)
				if err != nil {
					return fmt.Errorf(msg.Errors.DiscoverRulesForProvider, subDir, err)
				}
				for _, rule := range rules {
					l.index.AddRule(rule)
				}
			}

			if hasPacksDir {
				packs, err := DiscoverPacks(packsDir)
				if err != nil {
					return fmt.Errorf(msg.Errors.DiscoverPacksForProvider, subDir, err)
				}
				for _, pack := range packs {
					l.index.AddPack(pack)
				}
			}
		} else {
			// Flat structure: load .rego files directly from subdirectory
			// Check if directory contains .rego files
			if hasRegoFiles(subDirPath) {
				// Load lib modules if present
				if hasLibDir {
					l.loadLibModulesFromDir(libDir)
				}

				rules, err := DiscoverRulesWithExtraModules(subDirPath, l.extraModules)
				if err != nil {
					return fmt.Errorf(msg.Errors.DiscoverRulesForProvider, subDir, err)
				}
				for _, rule := range rules {
					l.index.AddRule(rule)
				}

				// Also try to discover packs in the same directory
				packs, err := DiscoverPacks(subDirPath)
				if err != nil {
					return fmt.Errorf(msg.Errors.DiscoverPacksForProvider, subDir, err)
				}
				for _, pack := range packs {
					l.index.AddPack(pack)
				}
			}
		}
	}

	return nil
}

// hasRegoFiles checks if a directory contains any .rego files.
func hasRegoFiles(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".rego") {
			return true
		}
	}
	return false
}

// GetIndex returns the loaded policy index.
func (l *Loader) GetIndex() *models.PolicyIndex {
	return l.index
}

// GetRule returns a rule by ID.
func (l *Loader) GetRule(id string) *models.Rule {
	return l.index.GetRule(id)
}

// GetPack returns a pack by ID.
func (l *Loader) GetPack(id string) *models.Pack {
	return l.index.GetPack(id)
}

// GetRulesForPack returns all rules for a given pack ID.
func (l *Loader) GetRulesForPack(packID string) []*models.Rule {
	return l.index.GetRulesForPack(packID)
}

// GetAllRules returns all loaded rules.
func (l *Loader) GetAllRules() []*models.Rule {
	return l.index.RuleList
}

// GetAllPacks returns all loaded packs.
func (l *Loader) GetAllPacks() []*models.Pack {
	return l.index.PackList
}

// GetLibModules returns all loaded library modules.
func (l *Loader) GetLibModules() map[string]string {
	if l.index.LibModules == nil {
		return make(map[string]string)
	}
	return l.index.LibModules
}

// loadLibModulesFromDir loads all helper modules from the lib directory and stores them in the index.
func (l *Loader) loadLibModulesFromDir(libDir string) {
	if !dirExists(libDir) {
		return
	}

	filepath.WalkDir(libDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".rego") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip files that can't be read
		}

		// Store lib module with its path as key
		l.index.LibModules[path] = string(content)
		return nil
	})
}

// MatchRules returns all rules matching the given pattern.
// The pattern supports `*` wildcard matching.
// Returns empty slice if no rules match.
func (l *Loader) MatchRules(pattern string) []*models.Rule {
	var matches []*models.Rule
	for _, rule := range l.index.RuleList {
		if MatchPattern(pattern, rule.ID) {
			matches = append(matches, rule)
		}
	}
	return matches
}

// MatchPacks returns all packs matching the given pattern.
// The pattern supports `*` wildcard matching.
// Returns empty slice if no packs match.
func (l *Loader) MatchPacks(pattern string) []*models.Pack {
	var matches []*models.Pack
	for _, pack := range l.index.PackList {
		if MatchPattern(pattern, pack.ID) {
			matches = append(matches, pack)
		}
	}
	return matches
}

// dirExists checks if a directory exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Manager handles policy library operations.
type Manager struct {
	policyDir string
}

// NewManager creates a new policy manager.
func NewManager(policyDir string) *Manager {
	return &Manager{policyDir: policyDir}
}

// Update downloads and updates the policy library.
// When repo is empty, it uses the default OSS policy archive. Otherwise it keeps
// backward-compatible git repository support.
func (m *Manager) Update(repo, version string) error {
	if strings.TrimSpace(repo) == "" {
		return m.UpdateFromOSS(version)
	}
	if isLatestPolicyVersion(version) {
		version = DefaultPolicyGitRef
	}
	return m.updateFromGit(repo, version)
}

func (m *Manager) updateFromGit(repo, version string) error {
	return m.updateFromGitFirstAvailable(repo, []string{version})
}

func (m *Manager) updateFromGitFirstAvailable(repo string, refs []string) error {
	msg := i18n.Msg()
	if len(refs) == 0 {
		refs = []string{DefaultPolicyGitRef}
	}

	var lastErr error
	for _, ref := range refs {
		tmpRoot, err := os.MkdirTemp("", "infraguard-policy-git-*")
		if err != nil {
			return fmt.Errorf(msg.Errors.CreatePolicyDirectory, err)
		}

		stagingDir := filepath.Join(tmpRoot, "policies")
		if err := downloadPoliciesFromGit(repo, ref, stagingDir); err != nil {
			lastErr = err
			_ = os.RemoveAll(tmpRoot)
			continue
		}

		err = replacePolicyDir(stagingDir, m.policyDir)
		_ = os.RemoveAll(tmpRoot)
		if err != nil {
			return fmt.Errorf(msg.Errors.DownloadPolicies, err)
		}
		return nil
	}

	return fmt.Errorf(msg.Errors.DownloadPolicies, lastErr)
}

func downloadPoliciesFromGit(repo, version, dst string) error {
	getterURL := buildGetterURL(repo, version)
	client := &getter.Client{
		Src:  getterURL,
		Dst:  dst,
		Mode: getter.ClientModeDir,
	}
	return client.Get()
}

// UpdateFromOSS downloads a versioned policy archive from OSS and atomically
// replaces the local policy directory only after download and extraction succeed.
func (m *Manager) UpdateFromOSS(version string) error {
	msg := i18n.Msg()

	resolvedVersion, err := resolvePolicyVersion(version)
	if err != nil {
		if isFallbackHTTPError(err) && isLatestPolicyVersion(version) {
			return m.updateFromGitFirstAvailable(resolvePolicyGitRepo(), []string{DefaultPolicyGitRef})
		}
		return fmt.Errorf(msg.Errors.DownloadPolicies, err)
	}

	tmpRoot, err := os.MkdirTemp("", "infraguard-policy-update-*")
	if err != nil {
		return fmt.Errorf(msg.Errors.CreatePolicyDirectory, err)
	}
	defer os.RemoveAll(tmpRoot)

	archivePath := filepath.Join(tmpRoot, fmt.Sprintf(policyArchiveNamePattern, resolvedVersion))
	archiveURL := fmt.Sprintf("%s%s/%s", resolvePolicyBaseURL(), resolvedVersion, filepath.Base(archivePath))
	if err := downloadFile(archiveURL, archivePath); err != nil {
		if isFallbackHTTPError(err) {
			return m.updateFromGitFirstAvailable(resolvePolicyGitRepo(), policyGitRefCandidates(resolvedVersion))
		}
		return fmt.Errorf(msg.Errors.DownloadPolicies, err)
	}

	extractDir := filepath.Join(tmpRoot, "extract")
	if err := extractPolicyArchive(archivePath, extractDir); err != nil {
		return fmt.Errorf(msg.Errors.DownloadPolicies, err)
	}

	sourceDir, err := archiveRootDir(extractDir)
	if err != nil {
		return fmt.Errorf(msg.Errors.DownloadPolicies, err)
	}

	if err := replacePolicyDir(sourceDir, m.policyDir); err != nil {
		return fmt.Errorf(msg.Errors.DownloadPolicies, err)
	}
	return nil
}

func resolvePolicyVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version != "" && version != DefaultPolicyVersion {
		return strings.TrimPrefix(version, "v"), nil
	}

	url := resolvePolicyBaseURL() + "version.txt"
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", httpStatusError{URL: url, StatusCode: resp.StatusCode}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read version file: %w", err)
	}
	resolved := strings.TrimSpace(string(body))
	if resolved == "" {
		return "", fmt.Errorf("version file is empty")
	}
	return strings.TrimPrefix(resolved, "v"), nil
}

func isLatestPolicyVersion(version string) bool {
	version = strings.TrimSpace(version)
	return version == "" || version == DefaultPolicyVersion
}

func downloadFile(url, dst string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: policyDownloadTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError{URL: url, StatusCode: resp.StatusCode}
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractPolicyArchive(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	tr := tar.NewReader(gr)
	var totalSize int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		cleanName, err := sanitizeArchivePath(hdr.Name)
		if err != nil || cleanName == "" {
			continue
		}
		localName, err := archiveLocalPath(cleanName)
		if err != nil {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(dst, localName), 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || hdr.Size > maxPolicyArchiveEntry {
				return fmt.Errorf("archive entry %s exceeds maximum size", cleanName)
			}
			totalSize += hdr.Size
			if totalSize > maxPolicyArchiveTotal {
				return fmt.Errorf("archive exceeds maximum extracted size")
			}
			filePath := filepath.Join(dst, localName)
			if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0600
			}
			out, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(out, io.LimitReader(tr, maxPolicyArchiveEntry+1))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written > maxPolicyArchiveEntry {
				return fmt.Errorf("archive entry %s exceeds maximum size", cleanName)
			}
		}
	}
	return nil
}

func archiveRootDir(extractDir string) (string, error) {
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("policy archive is empty")
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(extractDir, entries[0].Name()), nil
	}
	return extractDir, nil
}

func replacePolicyDir(sourceDir, policyDir string) error {
	if err := os.MkdirAll(filepath.Dir(policyDir), 0755); err != nil {
		return err
	}
	stagedDir := policyDir + ".new"
	backupDir := policyDir + ".old"
	_ = os.RemoveAll(stagedDir)
	_ = os.RemoveAll(backupDir)

	if err := os.Rename(sourceDir, stagedDir); err != nil {
		if err := copyDir(sourceDir, stagedDir); err != nil {
			_ = os.RemoveAll(stagedDir)
			return err
		}
	}

	hasExisting := dirExists(policyDir)
	if hasExisting {
		if err := os.Rename(policyDir, backupDir); err != nil {
			_ = os.RemoveAll(stagedDir)
			return err
		}
	}
	if err := os.Rename(stagedDir, policyDir); err != nil {
		_ = os.RemoveAll(policyDir)
		if hasExisting {
			_ = os.Rename(backupDir, policyDir)
		}
		_ = os.RemoveAll(stagedDir)
		return err
	}
	if hasExisting {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			return err
		}
		if err := in.Close(); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func sanitizeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid archive path")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "//") {
		return "", fmt.Errorf("absolute archive path is not allowed")
	}
	if len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':' {
		return "", fmt.Errorf("windows drive archive path is not allowed")
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	return clean, nil
}

func archiveLocalPath(cleanRel string) (string, error) {
	localRel, err := filepath.Localize(cleanRel)
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(localRel) {
		return "", fmt.Errorf("path is not local")
	}
	return localRel, nil
}

func resolvePolicyBaseURL() string {
	baseURL := strings.TrimSpace(os.Getenv(policyBaseURLEnv))
	if baseURL == "" {
		baseURL = defaultPolicyBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return baseURL
}

func resolvePolicyGitRepo() string {
	repo := strings.TrimSpace(os.Getenv(policyGitRepoEnv))
	if repo == "" {
		return defaultPolicyGitRepo
	}
	return repo
}

func policyGitRefCandidates(version string) []string {
	version = strings.TrimSpace(version)
	if version == "" || version == DefaultPolicyVersion {
		return []string{DefaultPolicyGitRef}
	}
	if strings.Contains(version, "/") {
		return []string{version}
	}

	trimmedV := strings.TrimPrefix(version, "v")
	candidates := []string{version}
	if version == trimmedV {
		candidates = append(candidates, "v"+trimmedV)
	} else {
		candidates = append(candidates, trimmedV)
	}
	candidates = append(candidates, "cli/v"+trimmedV)

	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

type httpStatusError struct {
	URL        string
	StatusCode int
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("%s returned status %d", e.URL, e.StatusCode)
}

func isFallbackHTTPError(err error) bool {
	var statusErr httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusForbidden
}

// Clean removes the policy directory and all its contents.
// If the directory doesn't exist, this is not considered an error.
func (m *Manager) Clean() error {
	msg := i18n.Msg()

	// Check if directory exists
	if _, err := os.Stat(m.policyDir); os.IsNotExist(err) {
		// Directory doesn't exist - this is fine
		return nil
	}

	// Remove the directory and all contents
	if err := os.RemoveAll(m.policyDir); err != nil {
		return fmt.Errorf(msg.Errors.CleanPolicyDirectory, err)
	}

	return nil
}

// buildGetterURL constructs a go-getter compatible URL from various repo formats.
// Supported formats:
// - host/path (e.g., github.com/aliyun/infraguard)
// - HTTPS URL (e.g., https://github.com/aliyun/infraguard.git)
// - SSH URL (e.g., ssh://git@github.com/aliyun/infraguard.git)
// - SCP-like (e.g., git@github.com:aliyun/infraguard.git)
func buildGetterURL(repo, version string) string {
	repo = strings.TrimSpace(repo)

	// Pattern for SCP-like format: git@host:org/repo.git
	scpPattern := regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

	var baseURL string

	switch {
	case strings.HasPrefix(repo, "https://"):
		// HTTPS URL
		baseURL = strings.TrimSuffix(repo, ".git")
	case strings.HasPrefix(repo, "file://"):
		// Local file URL, used by tests and local mirrors.
		baseURL = repo
	case strings.HasPrefix(repo, "ssh://"):
		// SSH URL - keep as is
		baseURL = strings.TrimSuffix(repo, ".git")
	case scpPattern.MatchString(repo):
		// SCP-like format: git@host:org/repo.git -> ssh://git@host/org/repo
		matches := scpPattern.FindStringSubmatch(repo)
		host := matches[1]
		path := strings.TrimSuffix(matches[2], ".git")
		baseURL = fmt.Sprintf("ssh://git@%s/%s", host, path)
	default:
		// Plain host/path format
		baseURL = "https://" + strings.TrimSuffix(repo, ".git")
	}

	// Build go-getter git subdirectory URL
	if !strings.HasPrefix(baseURL, "file://") && !strings.HasSuffix(baseURL, ".git") {
		baseURL += ".git"
	}
	return fmt.Sprintf("git::%s//policies?ref=%s", baseURL, version)
}

// ValidatePath checks if a policy path exists and is valid.
// It supports both directories (containing .rego files) and single .rego files.
func ValidatePath(path string) error {
	msg := i18n.Msg()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf(msg.Errors.PolicyPathDoesNotExist, path)
	}
	if err != nil {
		return err
	}

	// If it's a file, check if it's a .rego file
	if !info.IsDir() {
		if !strings.HasSuffix(path, ".rego") {
			return fmt.Errorf(msg.Errors.FileMustBeRego, path)
		}
		return nil
	}

	// It's a directory - check for .rego files
	hasRego := false
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".rego") {
			hasRego = true
			return filepath.SkipAll // Found at least one, stop walking
		}
		return nil
	})
	if err != nil {
		return err
	}

	if !hasRego {
		return fmt.Errorf(msg.Errors.NoRegoFilesInPolicyDirectory, path)
	}

	return nil
}

// DiscoverRegoFiles finds all .rego files from a path.
// If path is a directory, it recursively finds all .rego files.
// If path is a .rego file, it returns that single file.
func DiscoverRegoFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	// If it's a single .rego file, return it directly
	msg := i18n.Msg()
	if !info.IsDir() {
		if strings.HasSuffix(path, ".rego") {
			return []string{path}, nil
		}
		return nil, fmt.Errorf(msg.Errors.FileMustBeRego, path)
	}

	// It's a directory - walk and find all .rego files
	var files []string
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".rego") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// MatchPattern checks if an ID matches a wildcard pattern.
// The pattern supports `*` as a wildcard that matches zero or more characters.
// Examples:
//   - "rule:*" matches all rule IDs
//   - "rule:aliyun:ecs-*" matches "rule:aliyun:ecs-instance-no-public-ip"
//   - "rule:aliyun:*-multi-zone" matches "rule:aliyun:rds-instance-multi-zone"
func MatchPattern(pattern, id string) bool {
	// If pattern is exactly "*", match everything
	if pattern == "*" {
		return true
	}

	// If pattern doesn't contain "*", do exact match
	if !strings.Contains(pattern, "*") {
		return pattern == id
	}

	// Convert pattern to regex-like matching
	// Escape special regex characters except *
	parts := strings.Split(pattern, "*")
	if len(parts) == 0 {
		return true // "*" only
	}

	// Handle prefix match (no leading *)
	if !strings.HasPrefix(pattern, "*") {
		if !strings.HasPrefix(id, parts[0]) {
			return false
		}
		id = strings.TrimPrefix(id, parts[0])
		parts = parts[1:]
	}

	// Handle suffix match (no trailing *)
	if len(parts) > 0 && !strings.HasSuffix(pattern, "*") {
		lastPart := parts[len(parts)-1]
		if !strings.HasSuffix(id, lastPart) {
			return false
		}
		id = strings.TrimSuffix(id, lastPart)
		parts = parts[:len(parts)-1]
	}

	// Handle middle parts (substring matching)
	for _, part := range parts {
		if part == "" {
			continue // Consecutive *s
		}
		idx := strings.Index(id, part)
		if idx == -1 {
			return false
		}
		id = id[idx+len(part):]
	}

	return true
}
