package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

const (
	defaultUpdateBaseURL = "https://ros-public-tools.oss-cn-beijing.aliyuncs.com/github-releases/aliyun/infraguard/"
	defaultGitHubAPIURL  = "https://api.github.com/repos/aliyun/infraguard/releases"
	updateBaseURLEnv     = "INFRAGUARD_UPDATE_BASE_URL"
	githubAPIURLEnv      = "INFRAGUARD_GITHUB_API_URL"
	downloadTimeout      = 5 * time.Minute
	progressInterval     = 100 * time.Millisecond
	cliTagPrefix         = "cli/"
	maxArchiveEntrySize  = 200 << 20
)

// Release represents a published CLI release.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset represents a release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Updater handles CLI updates
type Updater struct {
	currentVersion string
	baseURL        string
	githubAPIURL   string
	httpClient     *http.Client
	progressFunc   ProgressFunc
}

// ProgressFunc is called during download to report progress
type ProgressFunc func(downloaded, total int64)

// New creates a new Updater instance
func New(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		baseURL:        resolveUpdateBaseURL(),
		githubAPIURL:   resolveGitHubAPIURL(),
		httpClient: &http.Client{
			Timeout: downloadTimeout,
		},
	}
}

// SetProgressFunc sets the progress callback function
func (u *Updater) SetProgressFunc(fn ProgressFunc) {
	u.progressFunc = fn
}

// GetLatestVersion fetches the latest release version from the OSS version file,
// falling back to GitHub releases when the OSS version file is not available.
func (u *Updater) GetLatestVersion() (string, error) {
	latest, err := u.getLatestVersionFromOSS()
	if err == nil {
		return latest, nil
	}
	if !isFallbackHTTPError(err) {
		return "", err
	}
	return u.getLatestVersionFromGitHub()
}

func (u *Updater) getLatestVersionFromOSS() (string, error) {
	req, err := http.NewRequest("GET", u.baseURL+"version.txt", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "infraguard/"+strings.TrimPrefix(u.currentVersion, "v"))

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", httpStatusError{URL: req.URL.String(), StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read latest version: %w", err)
	}
	latest := strings.TrimSpace(string(body))
	if latest == "" {
		return "", fmt.Errorf("version file is empty")
	}
	return strings.TrimPrefix(latest, "v"), nil
}

func (u *Updater) getLatestVersionFromGitHub() (string, error) {
	releases, err := u.fetchReleases()
	if err != nil {
		return "", err
	}

	for _, release := range releases {
		if !release.Prerelease && release.TagName != "" {
			v, ok := parseCLITag(release.TagName)
			if !ok {
				continue
			}
			return v, nil
		}
	}

	return "", fmt.Errorf("no stable release found")
}

// GetSpecificVersion fetches a specific release version from GitHub.
func (u *Updater) GetSpecificVersion(targetVersion string) (*Release, error) {
	releases, err := u.fetchReleases()
	if err != nil {
		return nil, err
	}

	targetVersion = strings.TrimPrefix(targetVersion, "v")

	for _, release := range releases {
		v, ok := parseCLITag(release.TagName)
		if !ok {
			continue
		}
		if v == targetVersion {
			return &release, nil
		}
	}

	return nil, fmt.Errorf("version %s not found", targetVersion)
}

// parseCLITag checks if a tag belongs to the CLI component (prefixed with "cli/")
// and returns the clean version string. For backward compatibility, tags in the
// plain "v0.x.x" format (without any "/" prefix) are also accepted.
func parseCLITag(tag string) (string, bool) {
	if strings.HasPrefix(tag, cliTagPrefix) {
		v := strings.TrimPrefix(tag, cliTagPrefix)
		v = strings.TrimPrefix(v, "v")
		return v, true
	}
	if !strings.Contains(tag, "/") {
		v := strings.TrimPrefix(tag, "v")
		return v, true
	}
	return "", false
}

// fetchReleases fetches all releases from GitHub API.
func (u *Updater) fetchReleases() ([]Release, error) {
	req, err := http.NewRequest("GET", u.githubAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API rate limit exceeded. Please try again later or authenticate with a token")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return releases, nil
}

// CompareVersions compares current version with target version
// Returns: -1 if current < target, 0 if equal, 1 if current > target
func (u *Updater) CompareVersions(targetVersion string) (int, error) {
	// Handle development versions
	if strings.Contains(u.currentVersion, "dev") || u.currentVersion == "0.0.0" {
		return -1, nil
	}

	current, err := version.NewVersion(u.currentVersion)
	if err != nil {
		return 0, fmt.Errorf("invalid current version: %w", err)
	}

	target, err := version.NewVersion(targetVersion)
	if err != nil {
		return 0, fmt.Errorf("invalid target version: %w", err)
	}

	// hashicorp/go-version Compare returns:
	// -1 if current < target
	//  0 if current == target
	//  1 if current > target
	// This matches our expected semantics
	if current.LessThan(target) {
		return -1, nil
	} else if current.Equal(target) {
		return 0, nil
	} else {
		return 1, nil
	}
}

// NeedsUpdate checks if an update is available
func (u *Updater) NeedsUpdate(targetVersion string) (bool, error) {
	cmp, err := u.CompareVersions(targetVersion)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// DetectPlatform returns the current OS and architecture
func DetectPlatform() (goos, goarch string) {
	return runtime.GOOS, runtime.GOARCH
}

// GetAssetName generates the expected raw binary asset name for the current platform.
// Format: infraguard-vVERSION-OS-ARCH[.exe]
func GetAssetName(version, goos, goarch string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("infraguard-v%s-%s-%s%s", version, goos, goarch, platformBinarySuffix(goos))
}

func legacyRawAssetName(version, goos, goarch string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("infraguard-%s-%s-%s%s", version, goos, goarch, platformBinarySuffix(goos))
}

func archiveAssetName(version, goos, goarch string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("infraguard-v%s-%s-%s.tar.gz", version, goos, goarch)
}

func legacyArchiveAssetName(version, goos, goarch string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("infraguard-%s-%s-%s.tar.gz", version, goos, goarch)
}

func platformBinarySuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// DownloadAndInstall downloads the binary and installs it
func (u *Updater) DownloadAndInstall(targetVersion string) error {
	// Detect platform
	goos, goarch := DetectPlatform()

	// Download the binary
	tmpFile, err := u.downloadUpdateAsset(targetVersion, goos, goarch)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpFile)

	// Replace current binary (tmpFile is the binary directly)
	if err := replaceBinary(tmpFile); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	return nil
}

func (u *Updater) downloadUpdateAsset(targetVersion, goos, goarch string) (string, error) {
	version := strings.TrimPrefix(targetVersion, "v")
	var ossErr error
	for _, assetName := range ossAssetNameCandidates(version, goos, goarch) {
		asset := &Asset{
			Name:               assetName,
			BrowserDownloadURL: fmt.Sprintf("%s%s/%s", u.baseURL, version, assetName),
		}

		tmpFile, err := u.downloadAsset(asset)
		if err == nil {
			return tmpFile, nil
		}
		if !isFallbackHTTPError(err) {
			return "", err
		}
		if ossErr == nil {
			ossErr = err
		}
	}

	tmpFile, githubErr := u.downloadGitHubAsset(version, goos, goarch)
	if githubErr != nil {
		return "", fmt.Errorf("OSS download failed: %v; GitHub fallback failed: %w", ossErr, githubErr)
	}
	return tmpFile, nil
}

func (u *Updater) downloadGitHubAsset(targetVersion, goos, goarch string) (string, error) {
	release, err := u.GetSpecificVersion(targetVersion)
	if err != nil {
		return "", err
	}

	expectedNames := githubAssetNameCandidates(targetVersion, goos, goarch)
	for _, expectedName := range expectedNames {
		for i := range release.Assets {
			if release.Assets[i].Name == expectedName {
				return u.downloadAsset(&release.Assets[i])
			}
		}
	}
	return "", fmt.Errorf("no binary found for %s/%s (expected one of: %s)", goos, goarch, strings.Join(expectedNames, ", "))
}

func githubAssetNameCandidates(version, goos, goarch string) []string {
	cleanVersion := strings.TrimPrefix(version, "v")

	candidates := []string{
		GetAssetName(cleanVersion, goos, goarch),
		legacyRawAssetName(cleanVersion, goos, goarch),
		archiveAssetName(cleanVersion, goos, goarch),
		legacyArchiveAssetName(cleanVersion, goos, goarch),
	}

	return uniqueAssetNameCandidates(candidates)
}

func ossAssetNameCandidates(version, goos, goarch string) []string {
	candidates := []string{
		GetAssetName(version, goos, goarch),
		legacyRawAssetName(version, goos, goarch),
		archiveAssetName(version, goos, goarch),
		legacyArchiveAssetName(version, goos, goarch),
	}

	return uniqueAssetNameCandidates(candidates)
}

func uniqueAssetNameCandidates(candidates []string) []string {
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

// downloadAsset downloads an asset to a temporary executable. tar.gz assets are
// extracted, while historical GitHub assets are raw binaries.
func (u *Updater) downloadAsset(asset *Asset) (string, error) {
	req, err := http.NewRequest("GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", httpStatusError{URL: req.URL.String(), StatusCode: resp.StatusCode}
	}

	if strings.HasSuffix(asset.Name, ".tar.gz") {
		return u.downloadArchiveAsset(resp, asset)
	}
	return u.downloadRawAsset(resp, asset)
}

func (u *Updater) downloadArchiveAsset(resp *http.Response, asset *Asset) (string, error) {
	archiveFile, err := os.CreateTemp("", "infraguard-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(archiveFile.Name())

	// Download with progress tracking
	var downloaded int64
	totalSize := asset.Size
	if totalSize <= 0 {
		totalSize = resp.ContentLength
	}

	reader := io.TeeReader(resp.Body, &progressWriter{
		total:    totalSize,
		current:  &downloaded,
		callback: u.progressFunc,
	})

	if _, err := io.Copy(archiveFile, reader); err != nil {
		archiveFile.Close()
		return "", err
	}
	if err := archiveFile.Close(); err != nil {
		return "", err
	}

	return extractBinaryFromArchive(archiveFile.Name())
}

func (u *Updater) downloadRawAsset(resp *http.Response, asset *Asset) (string, error) {
	tmpFile, err := os.CreateTemp("", "infraguard-update-*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to set executable permissions: %w", err)
	}

	var downloaded int64
	totalSize := asset.Size
	if totalSize <= 0 {
		totalSize = resp.ContentLength
	}

	reader := io.TeeReader(resp.Body, &progressWriter{
		total:    totalSize,
		current:  &downloaded,
		callback: u.progressFunc,
	})

	if _, err := io.Copy(tmpFile, reader); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}
	return tmpFile.Name(), nil
}

func extractBinaryFromArchive(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		if !isInfraGuardBinaryEntry(hdr.Name) {
			continue
		}
		if hdr.Size < 0 || hdr.Size > maxArchiveEntrySize {
			return "", fmt.Errorf("archive entry %s exceeds maximum size", hdr.Name)
		}

		tmpFile, err := os.CreateTemp("", "infraguard-update-*")
		if err != nil {
			return "", err
		}
		written, copyErr := io.Copy(tmpFile, io.LimitReader(tr, maxArchiveEntrySize+1))
		closeErr := tmpFile.Close()
		if copyErr != nil {
			os.Remove(tmpFile.Name())
			return "", copyErr
		}
		if closeErr != nil {
			os.Remove(tmpFile.Name())
			return "", closeErr
		}
		if written > maxArchiveEntrySize {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("archive entry %s exceeds maximum size", hdr.Name)
		}
		if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to set executable permissions: %w", err)
		}
		return tmpFile.Name(), nil
	}

	return "", fmt.Errorf("infraguard binary not found in archive")
}

func isInfraGuardBinaryEntry(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	base := path.Base(path.Clean(name))
	return base == "infraguard" || base == "infraguard.exe"
}

func resolveUpdateBaseURL() string {
	baseURL := strings.TrimSpace(os.Getenv(updateBaseURLEnv))
	if baseURL == "" {
		baseURL = defaultUpdateBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return baseURL
}

func resolveGitHubAPIURL() string {
	apiURL := strings.TrimSpace(os.Getenv(githubAPIURLEnv))
	if apiURL == "" {
		return defaultGitHubAPIURL
	}
	return apiURL
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

// progressWriter wraps io.Writer to track progress
type progressWriter struct {
	total    int64
	current  *int64
	callback ProgressFunc
	lastCall time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	*pw.current += int64(n)

	// Call progress callback with rate limiting
	if pw.callback != nil && time.Since(pw.lastCall) > progressInterval {
		pw.callback(*pw.current, pw.total)
		pw.lastCall = time.Now()
	}

	return n, nil
}

// replaceBinary replaces the current binary with the new one
func replaceBinary(newBinaryPath string) error {
	// Get current executable path
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	// Resolve symlinks (Unix-like systems)
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Windows requires special handling because running executables cannot be replaced
	if runtime.GOOS == "windows" {
		return replaceWindowsBinary(newBinaryPath, currentPath)
	}

	// Unix-like systems: direct replacement
	if err := replaceUnixBinary(newBinaryPath, currentPath); err != nil {
		return err
	}

	// On macOS, remove quarantine attributes to prevent Gatekeeper from blocking execution
	if runtime.GOOS == "darwin" {
		if err := removeQuarantineAttributes(currentPath); err != nil {
			return fmt.Errorf("failed to prepare binary for execution: %w", err)
		}
	}

	return nil
}

// replaceUnixBinary handles binary replacement on Unix-like systems
func replaceUnixBinary(newBinaryPath, currentPath string) error {
	// Create backup
	backupPath := currentPath + ".backup"
	if err := copyFile(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Attempt to replace binary
	if err := copyFile(newBinaryPath, currentPath); err != nil {
		// Rollback on failure
		_ = copyFile(backupPath, currentPath)
		os.Remove(backupPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	// Set executable permissions
	if err := os.Chmod(currentPath, 0755); err != nil {
		// Rollback on failure
		_ = copyFile(backupPath, currentPath)
		os.Remove(backupPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Clean up backup
	os.Remove(backupPath)

	return nil
}

// replaceWindowsBinary handles binary replacement on Windows
// Windows locks running executables, so we use a rename strategy:
// 1. Rename current binary to .old
// 2. Copy new binary to original location
// 3. The .old file will be deleted on next update or manually
func replaceWindowsBinary(newBinaryPath, currentPath string) error {
	oldPath := currentPath + ".old"

	// Remove any existing .old file from previous update
	if _, err := os.Stat(oldPath); err == nil {
		// Try to remove it, but don't fail if we can't
		// (it might be locked by another process)
		_ = os.Remove(oldPath)
	}

	// Rename current binary to .old
	// This works even for running executables on Windows
	if err := os.Rename(currentPath, oldPath); err != nil {
		return fmt.Errorf("failed to rename current binary: %w", err)
	}

	// Copy new binary to original location
	if err := copyFile(newBinaryPath, currentPath); err != nil {
		// Rollback: try to restore the original
		_ = os.Rename(oldPath, currentPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Success - the .old file will be cleaned up on next update
	// We can't delete it now because the current process might still be using it
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}

// removeQuarantineAttributes removes macOS quarantine attributes from a file
// and re-signs it to prevent Gatekeeper from blocking execution.
//
// This function is necessary because:
//  1. Files downloaded via HTTP (even through Go's http.Client) get automatically
//     tagged with com.apple.provenance extended attribute by macOS
//  2. GitHub releases binaries are not code-signed, so Gatekeeper rejects them
//  3. go install works because it compiles locally and Go automatically signs
//     the binary with an ad-hoc signature
//
// By clearing extended attributes and re-signing with ad-hoc signature, we
// make downloaded binaries behave like locally compiled ones.
func removeQuarantineAttributes(path string) error {
	// Step 1: Clear all extended attributes (quarantine, provenance, etc.)
	// This removes the "downloaded from internet" marker that macOS adds
	cmd := exec.Command("xattr", "-c", path)
	if err := cmd.Run(); err != nil {
		// If xattr command fails, try individual attributes
		_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
	}

	// Step 2: Re-sign the binary with ad-hoc signature
	// This is critical on macOS to prevent the system from killing the process
	cmd = exec.Command("codesign", "--force", "--deep", "--sign", "-", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to re-sign binary: %w", err)
	}

	return nil
}
