package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestUpdater(t *testing.T) {
	Convey("Given an Updater instance", t, func() {
		updater := New("0.2.0")

		Convey("When comparing versions", func() {
			Convey("Should detect newer version", func() {
				cmp, err := updater.CompareVersions("0.3.0")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
			})

			Convey("Should detect same version", func() {
				cmp, err := updater.CompareVersions("0.2.0")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 0)
			})

			Convey("Should detect older version", func() {
				cmp, err := updater.CompareVersions("0.1.0")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 1)
			})

			Convey("Should handle major version change", func() {
				cmp, err := updater.CompareVersions("1.0.0")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
			})

			Convey("Should handle patch version change", func() {
				cmp, err := updater.CompareVersions("0.2.1")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
			})
		})

		Convey("When checking if update is needed", func() {
			Convey("Should return true for newer version", func() {
				needed, err := updater.NeedsUpdate("0.3.0")
				So(err, ShouldBeNil)
				So(needed, ShouldBeTrue)
			})

			Convey("Should return false for same version", func() {
				needed, err := updater.NeedsUpdate("0.2.0")
				So(err, ShouldBeNil)
				So(needed, ShouldBeFalse)
			})

			Convey("Should return false for older version", func() {
				needed, err := updater.NeedsUpdate("0.1.0")
				So(err, ShouldBeNil)
				So(needed, ShouldBeFalse)
			})
		})
	})

	Convey("Given a dev version Updater", t, func() {
		updater := New("0.0.0-dev")

		Convey("When comparing with stable version", func() {
			Convey("Should always indicate update available", func() {
				cmp, err := updater.CompareVersions("0.3.0")
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
			})
		})
	})

	Convey("parseCLITag", t, func() {
		Convey("Should parse root module release tags as canonical CLI releases", func() {
			v, ok := parseCLITag("v0.11.0")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, "0.11.0")
		})

		Convey("Should parse cli/ prefixed tags", func() {
			v, ok := parseCLITag("cli/v0.2.0")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, "0.2.0")
		})

		Convey("Should parse cli/ tag without v prefix", func() {
			v, ok := parseCLITag("cli/0.3.0")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, "0.3.0")
		})

		Convey("Should accept plain v-prefixed tags for backward compatibility", func() {
			v, ok := parseCLITag("v0.1.0")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, "0.1.0")
		})

		Convey("Should accept plain version tags without v prefix", func() {
			v, ok := parseCLITag("0.1.0")
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, "0.1.0")
		})

		Convey("Should reject vscode/ prefixed tags", func() {
			_, ok := parseCLITag("vscode/v0.2.0")
			So(ok, ShouldBeFalse)
		})

		Convey("Should reject other prefixed tags", func() {
			_, ok := parseCLITag("server/v1.0.0")
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Release workflow", t, func() {
		Convey("Should publish CLI releases from root module version tags", func() {
			workflow, err := os.ReadFile("../../.github/workflows/release-cli.yml")
			So(err, ShouldBeNil)

			content := string(workflow)
			So(content, ShouldContainSubstring, "- 'v*'")
			So(content, ShouldNotContainSubstring, "- 'cli/v*'")
			So(content, ShouldContainSubstring, "VERSION=${RAW_VERSION#v}")
			So(content, ShouldContainSubstring, "SOURCE_VERSION=$(sed -nE")
			So(content, ShouldContainSubstring, "cmd/infraguard/cmd/version.go")
			So(content, ShouldContainSubstring, "Version mismatch: tag")
			So(content, ShouldContainSubstring, "RELEASE_WEBHOOK_URL")
			So(content, ShouldContainSubstring, "curl --silent --show-error")
			So(content, ShouldContainSubstring, `gh api "repos/${GITHUB_REPOSITORY}/releases/tags/${GITHUB_REF_NAME}"`)
			So(content, ShouldContainSubstring, `{action: "edited", release: $release[0], repository: $repository[0]}`)
			So(content, ShouldContainSubstring, "X-GitHub-Event: release")
			So(content, ShouldContainSubstring, "X-Hub-Signature-256: sha256=${signature}")
			So(content, ShouldContainSubstring, "X-Fc-Invocation-Type: Async")
			So(content, ShouldContainSubstring, "X-Fc-Async-Task-Id: ${task_id}")
			So(content, ShouldContainSubstring, `case "${http_code}" in`)
			So(content, ShouldContainSubstring, `409)`)
			So(content, ShouldNotContainSubstring, "refs/tags/cli/")
		})
	})

	Convey("Platform detection", t, func() {
		Convey("Should detect OS and architecture", func() {
			goos, goarch := DetectPlatform()
			So(goos, ShouldNotBeEmpty)
			So(goarch, ShouldNotBeEmpty)
			So(goos, ShouldBeIn, "darwin", "linux", "windows", "freebsd")
			So(goarch, ShouldBeIn, "amd64", "arm64", "386", "arm")
		})
	})

	Convey("OSS version discovery", t, func() {
		Convey("Should read latest version from version.txt", func() {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte("0.3.0\n"))
			}))
			defer server.Close()
			t.Setenv("INFRAGUARD_UPDATE_BASE_URL", server.URL+"/")

			updater := New("0.2.0")
			latest, err := updater.GetLatestVersion()

			So(err, ShouldBeNil)
			So(latest, ShouldEqual, "0.3.0")
			So(gotPath, ShouldEqual, "/version.txt")
		})

		Convey("Should fall back to GitHub releases when OSS version file is missing", func() {
			ossServer := httptest.NewServer(http.NotFoundHandler())
			defer ossServer.Close()
			t.Setenv("INFRAGUARD_UPDATE_BASE_URL", ossServer.URL+"/")

			var githubPath string
			githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				githubPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `[{"tag_name":"cli/v0.4.0","prerelease":false,"assets":[]}]`)
			}))
			defer githubServer.Close()
			t.Setenv("INFRAGUARD_GITHUB_API_URL", githubServer.URL+"/releases")

			updater := New("0.2.0")
			latest, err := updater.GetLatestVersion()

			So(err, ShouldBeNil)
			So(latest, ShouldEqual, "0.4.0")
			So(githubPath, ShouldEqual, "/releases")
		})
	})

	Convey("Asset name generation", t, func() {
		Convey("Should generate correct asset name", func() {
			name := GetAssetName("0.2.0", "darwin", "arm64")
			So(name, ShouldEqual, "infraguard-v0.2.0-darwin-arm64")
		})

		Convey("Should handle version without 'v' prefix", func() {
			name := GetAssetName("0.2.0", "linux", "amd64")
			So(name, ShouldEqual, "infraguard-v0.2.0-linux-amd64")
		})

		Convey("Should avoid duplicating 'v' prefix if present", func() {
			name := GetAssetName("v0.2.0", "windows", "amd64")
			So(name, ShouldEqual, "infraguard-v0.2.0-windows-amd64.exe")
		})
	})

	Convey("Archive download", t, func() {
		Convey("Should extract the infraguard binary from a tar.gz asset", func() {
			archive := buildUpdaterTarGz(t, map[string]string{
				"infraguard/infraguard": "#!/bin/sh\necho updated\n",
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(archive)
			}))
			defer server.Close()

			updater := New("0.2.0")
			tmpFile, err := updater.downloadAsset(&Asset{
				Name:               "infraguard-0.3.0-darwin-arm64.tar.gz",
				BrowserDownloadURL: server.URL + "/0.3.0/infraguard-0.3.0-darwin-arm64.tar.gz",
				Size:               int64(len(archive)),
			})
			So(err, ShouldBeNil)
			defer os.Remove(tmpFile)

			data, err := os.ReadFile(tmpFile)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "#!/bin/sh\necho updated\n")
		})

		Convey("Should download a legacy raw GitHub binary asset", func() {
			binary := "#!/bin/sh\necho legacy\n"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(binary))
			}))
			defer server.Close()

			updater := New("0.2.0")
			tmpFile, err := updater.downloadAsset(&Asset{
				Name:               "infraguard-v0.3.0-linux-amd64",
				BrowserDownloadURL: server.URL + "/download/infraguard-v0.3.0-linux-amd64",
				Size:               int64(len(binary)),
			})
			So(err, ShouldBeNil)
			defer os.Remove(tmpFile)

			data, err := os.ReadFile(tmpFile)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, binary)
		})

		Convey("Should download the v-prefixed OSS raw binary name first", func() {
			binary := "#!/bin/sh\necho oss raw binary\n"
			var downloadPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloadPath = r.URL.Path
				_, _ = w.Write([]byte(binary))
			}))
			defer server.Close()
			t.Setenv("INFRAGUARD_UPDATE_BASE_URL", server.URL+"/")

			updater := New("0.2.0")
			tmpFile, err := updater.downloadUpdateAsset("0.3.0", "linux", "amd64")
			So(err, ShouldBeNil)
			defer os.Remove(tmpFile)

			data, err := os.ReadFile(tmpFile)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, binary)
			So(downloadPath, ShouldEqual, "/0.3.0/infraguard-v0.3.0-linux-amd64")
		})

		Convey("Should fall back to a legacy OSS archive name when the raw binary is missing", func() {
			archive := buildUpdaterTarGz(t, map[string]string{
				"infraguard/infraguard": "#!/bin/sh\necho legacy oss archive\n",
			})
			var downloadPaths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloadPaths = append(downloadPaths, r.URL.Path)
				switch r.URL.Path {
				case "/0.3.0/infraguard-v0.3.0-linux-amd64":
					http.NotFound(w, r)
				case "/0.3.0/infraguard-0.3.0-linux-amd64":
					http.NotFound(w, r)
				case "/0.3.0/infraguard-v0.3.0-linux-amd64.tar.gz":
					http.NotFound(w, r)
				case "/0.3.0/infraguard-0.3.0-linux-amd64.tar.gz":
					_, _ = w.Write(archive)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("INFRAGUARD_UPDATE_BASE_URL", server.URL+"/")

			updater := New("0.2.0")
			tmpFile, err := updater.downloadUpdateAsset("0.3.0", "linux", "amd64")
			So(err, ShouldBeNil)
			defer os.Remove(tmpFile)

			data, err := os.ReadFile(tmpFile)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "#!/bin/sh\necho legacy oss archive\n")
			So(downloadPaths, ShouldResemble, []string{
				"/0.3.0/infraguard-v0.3.0-linux-amd64",
				"/0.3.0/infraguard-0.3.0-linux-amd64",
				"/0.3.0/infraguard-v0.3.0-linux-amd64.tar.gz",
				"/0.3.0/infraguard-0.3.0-linux-amd64.tar.gz",
			})
		})

		Convey("Should fall back to a GitHub release asset when the OSS archive is missing", func() {
			ossServer := httptest.NewServer(http.NotFoundHandler())
			defer ossServer.Close()
			t.Setenv("INFRAGUARD_UPDATE_BASE_URL", ossServer.URL+"/")

			binary := "#!/bin/sh\necho github fallback\n"
			var downloadPath string
			githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/releases":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `[{"tag_name":"cli/v0.3.0","prerelease":false,"assets":[{"name":"infraguard-v0.3.0-linux-amd64","browser_download_url":"%s/download/infraguard-v0.3.0-linux-amd64","size":%d}]}]`, "http://"+r.Host, len(binary))
				case "/download/infraguard-v0.3.0-linux-amd64":
					downloadPath = r.URL.Path
					_, _ = w.Write([]byte(binary))
				default:
					http.NotFound(w, r)
				}
			}))
			defer githubServer.Close()
			t.Setenv("INFRAGUARD_GITHUB_API_URL", githubServer.URL+"/releases")

			updater := New("0.2.0")
			tmpFile, err := updater.downloadUpdateAsset("0.3.0", "linux", "amd64")
			So(err, ShouldBeNil)
			defer os.Remove(tmpFile)

			data, err := os.ReadFile(tmpFile)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, binary)
			So(downloadPath, ShouldEqual, "/download/infraguard-v0.3.0-linux-amd64")
		})
	})
}

func buildUpdaterTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
