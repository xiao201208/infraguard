package policy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aliyun/infraguard/pkg/models"
	. "github.com/smartystreets/goconvey/convey"
)

func normalizeTestNewlines(data []byte) string {
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func TestDefaultPolicyDir(t *testing.T) {
	Convey("Given the DefaultPolicyDir function", t, func() {
		Convey("When INFRAGUARD_POLICY_DIR is set", func() {
			tmpDir, err := os.MkdirTemp("", "policy-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			oldEnv := os.Getenv("INFRAGUARD_POLICY_DIR")
			defer os.Setenv("INFRAGUARD_POLICY_DIR", oldEnv)

			os.Setenv("INFRAGUARD_POLICY_DIR", tmpDir)
			dir := DefaultPolicyDir()

			Convey("It should return the env var value", func() {
				So(dir, ShouldEqual, tmpDir)
			})
		})

		Convey("When INFRAGUARD_POLICY_DIR is not set", func() {
			oldEnv := os.Getenv("INFRAGUARD_POLICY_DIR")
			defer os.Setenv("INFRAGUARD_POLICY_DIR", oldEnv)

			os.Unsetenv("INFRAGUARD_POLICY_DIR")
			dir := DefaultPolicyDir()

			Convey("It should return a non-empty default path", func() {
				So(dir, ShouldNotBeEmpty)
			})

			Convey("It should contain .infraguard and policies", func() {
				So(dir, ShouldContainSubstring, ".infraguard")
				So(dir, ShouldContainSubstring, "policies")
			})
		})
	})
}

func TestNewManager(t *testing.T) {
	Convey("Given the NewManager function", t, func() {
		manager := NewManager("/test/path")

		Convey("It should return a non-nil manager", func() {
			So(manager, ShouldNotBeNil)
		})

		Convey("It should have the correct policyDir", func() {
			So(manager.policyDir, ShouldEqual, "/test/path")
		})
	})
}

func TestValidatePath(t *testing.T) {
	Convey("Given the ValidatePath function", t, func() {
		Convey("When path is a single .rego file", func() {
			tmpDir, err := os.MkdirTemp("", "policy-test-single")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			regoFile := filepath.Join(tmpDir, "policy.rego")
			err = os.WriteFile(regoFile, []byte("package test"), 0644)
			So(err, ShouldBeNil)

			err = ValidatePath(regoFile)

			Convey("It should succeed", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When path is a non-.rego file", func() {
			tmpDir, err := os.MkdirTemp("", "policy-test-nonrego")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			txtFile := filepath.Join(tmpDir, "policy.txt")
			err = os.WriteFile(txtFile, []byte("not a rego file"), 0644)
			So(err, ShouldBeNil)

			err = ValidatePath(txtFile)

			Convey("It should return an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}

func TestDiscoverRegoFiles(t *testing.T) {
	Convey("Given the DiscoverRegoFiles function", t, func() {
		Convey("When path is a single .rego file", func() {
			tmpDir, err := os.MkdirTemp("", "policy-test-discover-single")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			regoFile := filepath.Join(tmpDir, "single.rego")
			err = os.WriteFile(regoFile, []byte("package test"), 0644)
			So(err, ShouldBeNil)

			files, err := DiscoverRegoFiles(regoFile)

			Convey("It should return that file", func() {
				So(err, ShouldBeNil)
				So(len(files), ShouldEqual, 1)
				So(files[0], ShouldEqual, regoFile)
			})
		})

		Convey("When path is a non-.rego file", func() {
			tmpDir, err := os.MkdirTemp("", "policy-test-discover-nonrego")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			txtFile := filepath.Join(tmpDir, "policy.txt")
			err = os.WriteFile(txtFile, []byte("not a rego file"), 0644)
			So(err, ShouldBeNil)

			_, err = DiscoverRegoFiles(txtFile)

			Convey("It should return an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}

func TestBuildGetterURL(t *testing.T) {
	Convey("Given the buildGetterURL function", t, func() {
		tests := []struct {
			name     string
			repo     string
			version  string
			expected string
		}{
			{
				name:     "host/path format",
				repo:     "github.com/aliyun/infraguard",
				version:  "main",
				expected: "git::https://github.com/aliyun/infraguard.git//policies?ref=main",
			},
			{
				name:     "HTTPS URL",
				repo:     "https://github.com/aliyun/infraguard.git",
				version:  "v1.0.0",
				expected: "git::https://github.com/aliyun/infraguard.git//policies?ref=v1.0.0",
			},
			{
				name:     "HTTPS URL without .git",
				repo:     "https://github.com/aliyun/infraguard",
				version:  "main",
				expected: "git::https://github.com/aliyun/infraguard.git//policies?ref=main",
			},
			{
				name:     "SSH URL",
				repo:     "ssh://git@github.com/aliyun/infraguard.git",
				version:  "main",
				expected: "git::ssh://git@github.com/aliyun/infraguard.git//policies?ref=main",
			},
			{
				name:     "SCP-like format",
				repo:     "git@github.com:aliyun/infraguard.git",
				version:  "main",
				expected: "git::ssh://git@github.com/aliyun/infraguard.git//policies?ref=main",
			},
			{
				name:     "SCP-like format without .git",
				repo:     "git@github.com:aliyun/infraguard",
				version:  "develop",
				expected: "git::ssh://git@github.com/aliyun/infraguard.git//policies?ref=develop",
			},
			{
				name:     "file URL",
				repo:     "file:///tmp/infraguard-policy-repo",
				version:  "cli/v1.2.3",
				expected: "git::file:///tmp/infraguard-policy-repo//policies?ref=cli/v1.2.3",
			},
		}

		for _, tc := range tests {
			Convey("When repo is "+tc.name, func() {
				result := buildGetterURL(tc.repo, tc.version)

				Convey("It should return correct URL", func() {
					So(result, ShouldEqual, tc.expected)
				})
			})
		}

		Convey("When repo has whitespace", func() {
			result := buildGetterURL("  github.com/org/repo  ", "main")

			Convey("It should trim whitespace", func() {
				So(result, ShouldEqual, "git::https://github.com/org/repo.git//policies?ref=main")
			})
		})

		Convey("When SSH URL without .git", func() {
			result := buildGetterURL("ssh://git@github.com/org/repo", "v1.0")

			Convey("It should add .git suffix", func() {
				So(result, ShouldEqual, "git::ssh://git@github.com/org/repo.git//policies?ref=v1.0")
			})
		})
	})
}

func TestManagerUpdate(t *testing.T) {
	Convey("Given a Manager", t, func() {
		tmpDir, err := os.MkdirTemp("", "policy-update-test")
		So(err, ShouldBeNil)
		defer os.RemoveAll(tmpDir)

		manager := NewManager(tmpDir)

		Convey("When updating with invalid repository", func() {
			err := manager.Update("file:///nonexistent/path/that/does/not/exist", "main")

			Convey("It should return an error", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When updating a custom repository with the default policy version", func() {
			repoDir := initPolicyGitRepo(t, "cli/v1.2.3", map[string]string{
				"policies/aliyun/rules/from-main.rego": "package infraguard.rules.aliyun.from_main\n",
			})
			defer os.RemoveAll(repoDir)

			err := manager.Update("file://"+repoDir, DefaultPolicyVersion)

			Convey("It should keep the historical custom repository default of main", func() {
				So(err, ShouldBeNil)
				data, readErr := os.ReadFile(filepath.Join(tmpDir, "aliyun", "rules", "from-main.rego"))
				So(readErr, ShouldBeNil)
				So(normalizeTestNewlines(data), ShouldEqual, "package infraguard.rules.aliyun.from_main\n")
			})
		})
	})
}

func TestManagerUpdateFromOSS(t *testing.T) {
	Convey("Given a Manager using the default OSS policy source", t, func() {
		tmpDir, err := os.MkdirTemp("", "policy-oss-update-test")
		So(err, ShouldBeNil)
		defer os.RemoveAll(tmpDir)

		Convey("When updating a specific version", func() {
			archive := buildPolicyTarGz(t, map[string]string{
				"policies/aliyun/rules/demo.rego": "package infraguard.rules.aliyun.demo\n",
			})
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			t.Setenv("INFRAGUARD_POLICY_BASE_URL", server.URL+"/")

			manager := NewManager(tmpDir)
			err := manager.Update("", "1.2.3")

			Convey("It should install the extracted policy contents", func() {
				So(err, ShouldBeNil)
				So(gotPath, ShouldEqual, "/1.2.3/infraguard-policies-1.2.3.tar.gz")
				data, readErr := os.ReadFile(filepath.Join(tmpDir, "aliyun", "rules", "demo.rego"))
				So(readErr, ShouldBeNil)
				So(string(data), ShouldEqual, "package infraguard.rules.aliyun.demo\n")
			})
		})

		Convey("When the OSS archive cannot be downloaded", func() {
			oldPolicy := filepath.Join(tmpDir, "aliyun", "rules", "old.rego")
			err := os.MkdirAll(filepath.Dir(oldPolicy), 0755)
			So(err, ShouldBeNil)
			err = os.WriteFile(oldPolicy, []byte("old"), 0644)
			So(err, ShouldBeNil)

			server := httptest.NewServer(http.NotFoundHandler())
			defer server.Close()
			t.Setenv("INFRAGUARD_POLICY_BASE_URL", server.URL+"/")
			t.Setenv("INFRAGUARD_POLICY_GITHUB_REPO", "file:///nonexistent/path")

			manager := NewManager(tmpDir)
			err = manager.Update("", "1.2.3")

			Convey("It should keep the existing policy directory intact", func() {
				So(err, ShouldNotBeNil)
				data, readErr := os.ReadFile(oldPolicy)
				So(readErr, ShouldBeNil)
				So(string(data), ShouldEqual, "old")
			})
		})

		Convey("When the OSS archive is missing for a historical version", func() {
			repoDir := initPolicyGitRepo(t, "cli/v1.2.3", map[string]string{
				"policies/aliyun/rules/from-git.rego": "package infraguard.rules.aliyun.from_git\n",
			})
			defer os.RemoveAll(repoDir)
			t.Setenv("INFRAGUARD_POLICY_GITHUB_REPO", "file://"+repoDir)

			server := httptest.NewServer(http.NotFoundHandler())
			defer server.Close()
			t.Setenv("INFRAGUARD_POLICY_BASE_URL", server.URL+"/")

			manager := NewManager(tmpDir)
			err := manager.Update("", "1.2.3")

			Convey("It should fall back to the GitHub policy source and try the CLI tag", func() {
				So(err, ShouldBeNil)
				data, readErr := os.ReadFile(filepath.Join(tmpDir, "aliyun", "rules", "from-git.rego"))
				So(readErr, ShouldBeNil)
				So(normalizeTestNewlines(data), ShouldEqual, "package infraguard.rules.aliyun.from_git\n")
			})
		})
	})
}

func TestMatchPattern(t *testing.T) {
	Convey("Given the MatchPattern function", t, func() {
		Convey("When pattern is exactly '*'", func() {
			Convey("It should match all IDs", func() {
				So(MatchPattern("*", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeTrue)
				So(MatchPattern("*", "pack:aliyun:security-group-best-practice"), ShouldBeTrue)
				So(MatchPattern("*", "anything"), ShouldBeTrue)
			})
		})

		Convey("When pattern has no wildcard", func() {
			Convey("It should do exact match", func() {
				So(MatchPattern("rule:aliyun:ecs-instance-no-public-ip", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:ecs-instance-no-public-ip", "rule:aliyun:rds-instance-enabled-tde"), ShouldBeFalse)
			})
		})

		Convey("When pattern has prefix wildcard", func() {
			Convey("It should match prefix patterns", func() {
				So(MatchPattern("rule:aliyun:ecs-*", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:ecs-*", "rule:aliyun:ecs-security-group-not-open-all-port"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:ecs-*", "rule:aliyun:rds-instance-enabled-tde"), ShouldBeFalse)
			})
		})

		Convey("When pattern has suffix wildcard", func() {
			Convey("It should match suffix patterns", func() {
				So(MatchPattern("rule:aliyun:*-multi-zone", "rule:aliyun:rds-instance-multi-zone"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*-multi-zone", "rule:aliyun:ecs-instance-multi-zone"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*-multi-zone", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeFalse)
			})
		})

		Convey("When pattern has substring wildcard", func() {
			Convey("It should match substring patterns", func() {
				So(MatchPattern("rule:aliyun:*multi*", "rule:aliyun:rds-instance-multi-zone"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*multi*", "rule:aliyun:ecs-instance-multi-zone"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*multi*", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeFalse)
			})
		})

		Convey("When pattern has multiple wildcards", func() {
			Convey("It should match complex patterns", func() {
				So(MatchPattern("rule:aliyun:*instance*", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*instance*", "rule:aliyun:rds-instance-enabled-tde"), ShouldBeTrue)
				So(MatchPattern("rule:aliyun:*instance*", "rule:aliyun:security-group"), ShouldBeFalse)
			})
		})

		Convey("When pattern matches pack IDs", func() {
			Convey("It should match pack patterns", func() {
				So(MatchPattern("pack:aliyun:ecs-*", "pack:aliyun:ecs-best-practice"), ShouldBeTrue)
				So(MatchPattern("pack:aliyun:ecs-*", "pack:aliyun:ecs-security-baseline"), ShouldBeTrue)
				So(MatchPattern("pack:aliyun:*-compliance-pack", "pack:aliyun:mlps-level-3-pre-check-compliance-pack"), ShouldBeTrue)
			})
		})

		Convey("When pattern is empty", func() {
			Convey("It should only match empty string", func() {
				So(MatchPattern("", ""), ShouldBeTrue)
				So(MatchPattern("", "rule:aliyun:ecs-instance-no-public-ip"), ShouldBeFalse)
			})
		})
	})
}

func TestLoaderMatchRules(t *testing.T) {
	Convey("Given a Loader with rules", t, func() {
		loader := &Loader{
			index: &models.PolicyIndex{
				Rules:    make(map[string]*models.Rule),
				Packs:    make(map[string]*models.Pack),
				RuleList: []*models.Rule{},
				PackList: []*models.Pack{},
			},
		}

		// Add test rules
		rules := []*models.Rule{
			{ID: "rule:aliyun:ecs-instance-no-public-ip"},
			{ID: "rule:aliyun:ecs-security-group-not-open-all-port"},
			{ID: "rule:aliyun:rds-instance-enabled-tde"},
			{ID: "rule:aliyun:rds-instance-multi-zone"},
		}
		for _, rule := range rules {
			loader.index.AddRule(rule)
		}

		Convey("When matching with prefix pattern", func() {
			matches := loader.MatchRules("rule:aliyun:ecs-*")

			Convey("It should return matching rules", func() {
				So(len(matches), ShouldEqual, 2)
				ruleIDs := make(map[string]bool)
				for _, rule := range matches {
					ruleIDs[rule.ID] = true
				}
				So(ruleIDs["rule:aliyun:ecs-instance-no-public-ip"], ShouldBeTrue)
				So(ruleIDs["rule:aliyun:ecs-security-group-not-open-all-port"], ShouldBeTrue)
			})
		})

		Convey("When matching with suffix pattern", func() {
			matches := loader.MatchRules("rule:aliyun:*-multi-zone")

			Convey("It should return matching rules", func() {
				So(len(matches), ShouldEqual, 1)
				So(matches[0].ID, ShouldEqual, "rule:aliyun:rds-instance-multi-zone")
			})
		})

		Convey("When matching with wildcard pattern", func() {
			matches := loader.MatchRules("rule:*")

			Convey("It should return all rules", func() {
				So(len(matches), ShouldEqual, 4)
			})
		})

		Convey("When matching with pattern that matches nothing", func() {
			matches := loader.MatchRules("rule:aliyun:nonexistent-*")

			Convey("It should return empty slice", func() {
				So(len(matches), ShouldEqual, 0)
			})
		})
	})
}

func buildPolicyTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0644,
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

func initPolicyGitRepo(t *testing.T, tag string, files map[string]string) string {
	t.Helper()

	repoDir, err := os.MkdirTemp("", "policy-git-repo-*.git")
	if err != nil {
		t.Fatalf("create git repo dir: %v", err)
	}
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "checkout", "-b", "main")

	for name, body := range files {
		path := filepath.Join(repoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("create policy dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("write policy file: %v", err)
		}
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add policies")
	runGit(t, repoDir, "tag", tag)
	return repoDir
}

func runGit(t *testing.T, repoDir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func TestLoaderMatchPacks(t *testing.T) {
	Convey("Given a Loader with packs", t, func() {
		loader := &Loader{
			index: &models.PolicyIndex{
				Rules:    make(map[string]*models.Rule),
				Packs:    make(map[string]*models.Pack),
				RuleList: []*models.Rule{},
				PackList: []*models.Pack{},
			},
		}

		// Add test packs
		packs := []*models.Pack{
			{ID: "pack:aliyun:ecs-best-practice"},
			{ID: "pack:aliyun:ecs-security-baseline"},
			{ID: "pack:aliyun:mlps-level-3-pre-check-compliance-pack"},
		}
		for _, pack := range packs {
			loader.index.AddPack(pack)
		}

		Convey("When matching with prefix pattern", func() {
			matches := loader.MatchPacks("pack:aliyun:ecs-*")

			Convey("It should return matching packs", func() {
				So(len(matches), ShouldEqual, 2)
				packIDs := make(map[string]bool)
				for _, pack := range matches {
					packIDs[pack.ID] = true
				}
				So(packIDs["pack:aliyun:ecs-best-practice"], ShouldBeTrue)
				So(packIDs["pack:aliyun:ecs-security-baseline"], ShouldBeTrue)
			})
		})

		Convey("When matching with suffix pattern", func() {
			matches := loader.MatchPacks("pack:aliyun:*-compliance-pack")

			Convey("It should return matching packs", func() {
				So(len(matches), ShouldEqual, 1)
				So(matches[0].ID, ShouldEqual, "pack:aliyun:mlps-level-3-pre-check-compliance-pack")
			})
		})

		Convey("When matching with wildcard pattern", func() {
			matches := loader.MatchPacks("pack:*")

			Convey("It should return all packs", func() {
				So(len(matches), ShouldEqual, 3)
			})
		})

		Convey("When matching with pattern that matches nothing", func() {
			matches := loader.MatchPacks("pack:aliyun:nonexistent-*")

			Convey("It should return empty slice", func() {
				So(len(matches), ShouldEqual, 0)
			})
		})
	})
}

func TestWorkspacePolicyDir(t *testing.T) {
	Convey("Given the WorkspacePolicyDir function", t, func() {
		Convey("When INFRAGUARD_WORKSPACE_POLICY_DIR is set", func() {
			tmpDir, err := os.MkdirTemp("", "workspace-policy-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(tmpDir)

			oldEnv := os.Getenv("INFRAGUARD_WORKSPACE_POLICY_DIR")
			defer os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", oldEnv)

			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", tmpDir)
			dir := WorkspacePolicyDir()

			Convey("It should return the env var value", func() {
				So(dir, ShouldEqual, tmpDir)
			})
		})

		Convey("When INFRAGUARD_WORKSPACE_POLICY_DIR is not set", func() {
			oldEnv := os.Getenv("INFRAGUARD_WORKSPACE_POLICY_DIR")
			defer os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", oldEnv)

			os.Unsetenv("INFRAGUARD_WORKSPACE_POLICY_DIR")
			dir := WorkspacePolicyDir()

			Convey("It should return a non-empty default path", func() {
				So(dir, ShouldNotBeEmpty)
			})

			Convey("It should contain .infraguard and policies", func() {
				So(dir, ShouldContainSubstring, ".infraguard")
				So(dir, ShouldContainSubstring, "policies")
			})

			Convey("It should be an absolute path based on cwd", func() {
				cwd, err := os.Getwd()
				So(err, ShouldBeNil)
				expectedPath := filepath.Join(cwd, ".infraguard", "policies")
				So(dir, ShouldEqual, expectedPath)
			})
		})
	})
}

func TestLoadFlatDirectoryStructure(t *testing.T) {
	Convey("Given a flat directory structure with .rego files directly in subdirectory", t, func() {
		// Save original env vars
		oldPolicyDir := os.Getenv("INFRAGUARD_POLICY_DIR")
		oldWorkspaceDir := os.Getenv("INFRAGUARD_WORKSPACE_POLICY_DIR")
		defer func() {
			os.Setenv("INFRAGUARD_POLICY_DIR", oldPolicyDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", oldWorkspaceDir)
		}()

		Convey("When policies are placed directly in a subdirectory (flat structure)", func() {
			// Create temp directories
			workspaceDir, err := os.MkdirTemp("", "flat-structure-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(workspaceDir)

			// Create flat structure: my-rules/*.rego (no rules/ subdirectory)
			flatRulesDir := filepath.Join(workspaceDir, "my-custom-rules")
			err = os.MkdirAll(flatRulesDir, 0755)
			So(err, ShouldBeNil)

			flatRule := `package infraguard.rules.custom.flat_test_rule

import rego.v1

rule_meta := {
	"id": "rule:custom:flat-test-rule",
	"name": {"en": "Flat Structure Test Rule", "zh": "扁平结构测试规则"},
	"severity": "medium",
	"description": {"en": "Test rule in flat directory", "zh": "扁平目录中的测试规则"},
	"reason": {"en": "Test reason", "zh": "测试原因"},
	"recommendation": {"en": "Test recommendation", "zh": "测试建议"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(flatRulesDir, "flat-test-rule.rego"), []byte(flatRule), 0644)
			So(err, ShouldBeNil)

			// Set env vars (no user policies)
			emptyDir, err := os.MkdirTemp("", "empty-user-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(emptyDir)

			os.Setenv("INFRAGUARD_POLICY_DIR", emptyDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", workspaceDir)

			// Load policies
			loader, err := LoadWithFallback()
			So(err, ShouldBeNil)
			So(loader, ShouldNotBeNil)

			Convey("It should load the rule from flat directory structure", func() {
				rule := loader.GetRule("rule:custom:flat-test-rule")
				So(rule, ShouldNotBeNil)
				So(rule.Name.Get("en"), ShouldEqual, "Flat Structure Test Rule")
			})
		})

		Convey("When using provider-first structure (provider/rules/)", func() {
			// Create temp directories
			workspaceDir, err := os.MkdirTemp("", "provider-first-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(workspaceDir)

			// Create provider-first structure: aliyun/rules/*.rego
			providerRulesDir := filepath.Join(workspaceDir, "aliyun", "rules")
			err = os.MkdirAll(providerRulesDir, 0755)
			So(err, ShouldBeNil)

			providerRule := `package infraguard.rules.aliyun.provider_test_rule

import rego.v1

rule_meta := {
	"id": "rule:aliyun:provider-test-rule",
	"name": {"en": "Provider Structure Test Rule", "zh": "Provider结构测试规则"},
	"severity": "high",
	"description": {"en": "Test rule in provider directory", "zh": "Provider目录中的测试规则"},
	"reason": {"en": "Test reason", "zh": "测试原因"},
	"recommendation": {"en": "Test recommendation", "zh": "测试建议"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(providerRulesDir, "provider-test-rule.rego"), []byte(providerRule), 0644)
			So(err, ShouldBeNil)

			// Set env vars (no user policies)
			emptyDir, err := os.MkdirTemp("", "empty-user-test2")
			So(err, ShouldBeNil)
			defer os.RemoveAll(emptyDir)

			os.Setenv("INFRAGUARD_POLICY_DIR", emptyDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", workspaceDir)

			// Load policies
			loader, err := LoadWithFallback()
			So(err, ShouldBeNil)
			So(loader, ShouldNotBeNil)

			Convey("It should load the rule from provider-first structure", func() {
				rule := loader.GetRule("rule:aliyun:provider-test-rule")
				So(rule, ShouldNotBeNil)
				So(rule.Name.Get("en"), ShouldEqual, "Provider Structure Test Rule")
			})
		})
	})
}

func TestGenerateIDPrefix(t *testing.T) {
	Convey("Given the GenerateIDPrefix function", t, func() {
		Convey("When file is directly in baseDir (provider-first structure)", func() {
			tests := []struct {
				name     string
				filePath string
				baseDir  string
				idType   string
				expected string
			}{
				{
					name:     "rule in policies/aliyun/rules/",
					filePath: "policies/aliyun/rules/ecs-public-ip.rego",
					baseDir:  "policies/aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
				{
					name:     "pack in policies/aliyun/packs/",
					filePath: "policies/aliyun/packs/security-group-best-practice.rego",
					baseDir:  "policies/aliyun/packs",
					idType:   "pack",
					expected: "pack:aliyun:",
				},
				{
					name:     "rule in aliyun/rules/ (embedded path)",
					filePath: "aliyun/rules/ecs-public-ip.rego",
					baseDir:  "aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
				{
					name:     "pack in aliyun/packs/ (embedded path)",
					filePath: "aliyun/packs/security-baseline.rego",
					baseDir:  "aliyun/packs",
					idType:   "pack",
					expected: "pack:aliyun:",
				},
			}

			for _, tc := range tests {
				Convey("When "+tc.name, func() {
					result := GenerateIDPrefix(tc.filePath, tc.baseDir, tc.idType)

					Convey("It should return correct prefix", func() {
						So(result, ShouldEqual, tc.expected)
					})
				})
			}
		})

		Convey("When file is in IaC type subdirectory (ros/terraform excluded from ID)", func() {
			tests := []struct {
				name     string
				filePath string
				baseDir  string
				idType   string
				expected string
			}{
				{
					name:     "rule in ros subdirectory",
					filePath: "policies/aliyun/rules/ros/ecs-public-ip.rego",
					baseDir:  "policies/aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
				{
					name:     "rule in terraform subdirectory",
					filePath: "policies/aliyun/rules/terraform/ecs-instance-public-ip.rego",
					baseDir:  "policies/aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
				{
					name:     "rule in ros subdirectory (embedded path)",
					filePath: "aliyun/rules/ros/ecs-public-ip.rego",
					baseDir:  "aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
				{
					name:     "rule in terraform subdirectory (embedded path)",
					filePath: "aliyun/rules/terraform/ecs-instance-public-ip.rego",
					baseDir:  "aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:",
				},
			}

			for _, tc := range tests {
				Convey("When "+tc.name, func() {
					result := GenerateIDPrefix(tc.filePath, tc.baseDir, tc.idType)

					Convey("It should return correct prefix", func() {
						So(result, ShouldEqual, tc.expected)
					})
				})
			}
		})

		Convey("When file is in subdirectory of baseDir", func() {
			tests := []struct {
				name     string
				filePath string
				baseDir  string
				idType   string
				expected string
			}{
				{
					name:     "rule in subdirectory",
					filePath: "policies/aliyun/rules/ecs/public-ip.rego",
					baseDir:  "policies/aliyun/rules",
					idType:   "rule",
					expected: "rule:aliyun:ecs:",
				},
				{
					name:     "pack in subdirectory",
					filePath: "policies/aliyun/packs/security/baseline.rego",
					baseDir:  "policies/aliyun/packs",
					idType:   "pack",
					expected: "pack:aliyun:security:",
				},
			}

			for _, tc := range tests {
				Convey("When "+tc.name, func() {
					result := GenerateIDPrefix(tc.filePath, tc.baseDir, tc.idType)

					Convey("It should return correct prefix", func() {
						So(result, ShouldEqual, tc.expected)
					})
				})
			}
		})

		Convey("When baseDir is just provider name", func() {
			result := GenerateIDPrefix("custom/my-rule.rego", "custom", "rule")

			Convey("It should use provider as prefix", func() {
				So(result, ShouldEqual, "rule:custom:")
			})
		})
	})
}

func TestGenerateRuleID(t *testing.T) {
	Convey("Given the GenerateRuleID function", t, func() {
		Convey("When generating rule ID from provider-first structure", func() {
			tests := []struct {
				name     string
				filePath string
				baseDir  string
				ruleName string
				expected string
			}{
				{
					name:     "standard rule in policies/aliyun/rules/",
					filePath: "policies/aliyun/rules/ecs-public-ip.rego",
					baseDir:  "policies/aliyun/rules",
					ruleName: "ecs-public-ip",
					expected: "rule:aliyun:ecs-public-ip",
				},
				{
					name:     "rule with embedded path",
					filePath: "aliyun/rules/rds-instance-multi-zone.rego",
					baseDir:  "aliyun/rules",
					ruleName: "rds-instance-multi-zone",
					expected: "rule:aliyun:rds-instance-multi-zone",
				},
			}

			for _, tc := range tests {
				Convey("When "+tc.name, func() {
					result := GenerateRuleID(tc.filePath, tc.baseDir, tc.ruleName)

					Convey("It should return correct rule ID", func() {
						So(result, ShouldEqual, tc.expected)
					})
				})
			}
		})
	})
}

func TestGeneratePackID(t *testing.T) {
	Convey("Given the GeneratePackID function", t, func() {
		Convey("When generating pack ID from provider-first structure", func() {
			tests := []struct {
				name     string
				filePath string
				baseDir  string
				packName string
				expected string
			}{
				{
					name:     "standard pack in policies/aliyun/packs/",
					filePath: "policies/aliyun/packs/security-group-best-practice.rego",
					baseDir:  "policies/aliyun/packs",
					packName: "security-group-best-practice",
					expected: "pack:aliyun:security-group-best-practice",
				},
				{
					name:     "pack with embedded path",
					filePath: "aliyun/packs/ecs-best-practice.rego",
					baseDir:  "aliyun/packs",
					packName: "ecs-best-practice",
					expected: "pack:aliyun:ecs-best-practice",
				},
				{
					name:     "pack with multi-part name",
					filePath: "policies/aliyun/packs/mlps-level-3-pre-check-compliance-pack.rego",
					baseDir:  "policies/aliyun/packs",
					packName: "mlps-level-3-pre-check-compliance-pack",
					expected: "pack:aliyun:mlps-level-3-pre-check-compliance-pack",
				},
			}

			for _, tc := range tests {
				Convey("When "+tc.name, func() {
					result := GeneratePackID(tc.filePath, tc.baseDir, tc.packName)

					Convey("It should return correct pack ID", func() {
						So(result, ShouldEqual, tc.expected)
					})
				})
			}
		})
	})
}

func TestLoadWithFallbackPriority(t *testing.T) {
	Convey("Given LoadWithFallback with multiple policy sources", t, func() {
		// Save original env vars
		oldPolicyDir := os.Getenv("INFRAGUARD_POLICY_DIR")
		oldWorkspaceDir := os.Getenv("INFRAGUARD_WORKSPACE_POLICY_DIR")
		defer func() {
			os.Setenv("INFRAGUARD_POLICY_DIR", oldPolicyDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", oldWorkspaceDir)
		}()

		Convey("When workspace policies override user-local policies", func() {
			// Create temp directories
			userDir, err := os.MkdirTemp("", "user-policy-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(userDir)

			workspaceDir, err := os.MkdirTemp("", "workspace-policy-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(workspaceDir)

			// Create user-local rule
			userRulesDir := filepath.Join(userDir, "aliyun", "rules")
			err = os.MkdirAll(userRulesDir, 0755)
			So(err, ShouldBeNil)
			userRule := `package infraguard.rules.aliyun.test_rule

import rego.v1

rule_meta := {
	"id": "rule:aliyun:test-rule",
	"name": {"en": "User Test Rule", "zh": "用户测试规则"},
	"severity": "medium",
	"description": {"en": "User rule description", "zh": "用户规则描述"},
	"reason": {"en": "User reason", "zh": "用户原因"},
	"recommendation": {"en": "User recommendation", "zh": "用户建议"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(userRulesDir, "test-rule.rego"), []byte(userRule), 0644)
			So(err, ShouldBeNil)

			// Create workspace rule with same ID but different name
			workspaceRulesDir := filepath.Join(workspaceDir, "aliyun", "rules")
			err = os.MkdirAll(workspaceRulesDir, 0755)
			So(err, ShouldBeNil)
			workspaceRule := `package infraguard.rules.aliyun.test_rule

import rego.v1

rule_meta := {
	"id": "rule:aliyun:test-rule",
	"name": {"en": "Workspace Test Rule", "zh": "工作区测试规则"},
	"severity": "high",
	"description": {"en": "Workspace rule description", "zh": "工作区规则描述"},
	"reason": {"en": "Workspace reason", "zh": "工作区原因"},
	"recommendation": {"en": "Workspace recommendation", "zh": "工作区建议"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(workspaceRulesDir, "test-rule.rego"), []byte(workspaceRule), 0644)
			So(err, ShouldBeNil)

			// Set env vars
			os.Setenv("INFRAGUARD_POLICY_DIR", userDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", workspaceDir)

			// Load policies
			loader, err := LoadWithFallback()
			So(err, ShouldBeNil)
			So(loader, ShouldNotBeNil)

			Convey("It should return the workspace version of the rule", func() {
				rule := loader.GetRule("rule:aliyun:test-rule")
				So(rule, ShouldNotBeNil)
				So(rule.Name.Get("en"), ShouldEqual, "Workspace Test Rule")
				So(rule.Severity, ShouldEqual, "high")
			})
		})

		Convey("When only user-local policies exist", func() {
			// Create temp directories
			userDir, err := os.MkdirTemp("", "user-only-policy-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(userDir)

			emptyDir, err := os.MkdirTemp("", "empty-workspace-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(emptyDir)

			// Create user-local rule
			userRulesDir := filepath.Join(userDir, "aliyun", "rules")
			err = os.MkdirAll(userRulesDir, 0755)
			So(err, ShouldBeNil)
			userRule := `package infraguard.rules.aliyun.user_only_rule

import rego.v1

rule_meta := {
	"id": "rule:aliyun:user-only-rule",
	"name": {"en": "User Only Rule", "zh": "仅用户规则"},
	"severity": "low",
	"description": {"en": "User only description", "zh": "仅用户描述"},
	"reason": {"en": "User only reason", "zh": "仅用户原因"},
	"recommendation": {"en": "User only recommendation", "zh": "仅用户建议"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(userRulesDir, "user-only-rule.rego"), []byte(userRule), 0644)
			So(err, ShouldBeNil)

			// Set env vars (workspace dir doesn't contain policies subdirectory)
			os.Setenv("INFRAGUARD_POLICY_DIR", userDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", emptyDir)

			// Load policies
			loader, err := LoadWithFallback()
			So(err, ShouldBeNil)
			So(loader, ShouldNotBeNil)

			Convey("It should return the user-local rule", func() {
				rule := loader.GetRule("rule:aliyun:user-only-rule")
				So(rule, ShouldNotBeNil)
				So(rule.Name.Get("en"), ShouldEqual, "User Only Rule")
			})
		})

		Convey("When policies from multiple sources are merged", func() {
			// Create temp directories
			userDir, err := os.MkdirTemp("", "user-merge-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(userDir)

			workspaceDir, err := os.MkdirTemp("", "workspace-merge-test")
			So(err, ShouldBeNil)
			defer os.RemoveAll(workspaceDir)

			// Create user-local rule A
			userRulesDir := filepath.Join(userDir, "aliyun", "rules")
			err = os.MkdirAll(userRulesDir, 0755)
			So(err, ShouldBeNil)
			userRuleA := `package infraguard.rules.aliyun.rule_a

import rego.v1

rule_meta := {
	"id": "rule:aliyun:rule-a",
	"name": {"en": "Rule A from User", "zh": "规则A来自用户"},
	"severity": "medium",
	"description": {"en": "Rule A", "zh": "规则A"},
	"reason": {"en": "Reason A", "zh": "原因A"},
	"recommendation": {"en": "Recommendation A", "zh": "建议A"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(userRulesDir, "rule-a.rego"), []byte(userRuleA), 0644)
			So(err, ShouldBeNil)

			// Create workspace rule B
			workspaceRulesDir := filepath.Join(workspaceDir, "aliyun", "rules")
			err = os.MkdirAll(workspaceRulesDir, 0755)
			So(err, ShouldBeNil)
			workspaceRuleB := `package infraguard.rules.aliyun.rule_b

import rego.v1

rule_meta := {
	"id": "rule:aliyun:rule-b",
	"name": {"en": "Rule B from Workspace", "zh": "规则B来自工作区"},
	"severity": "high",
	"description": {"en": "Rule B", "zh": "规则B"},
	"reason": {"en": "Reason B", "zh": "原因B"},
	"recommendation": {"en": "Recommendation B", "zh": "建议B"},
	"resource_types": ["ALIYUN::ECS::Instance"],
}

deny contains result if {
	false
	result := {"id": rule_meta.id, "resource_id": "test", "violation_path": [], "meta": {"severity": rule_meta.severity, "reason": rule_meta.reason, "recommendation": rule_meta.recommendation}}
}
`
			err = os.WriteFile(filepath.Join(workspaceRulesDir, "rule-b.rego"), []byte(workspaceRuleB), 0644)
			So(err, ShouldBeNil)

			// Set env vars
			os.Setenv("INFRAGUARD_POLICY_DIR", userDir)
			os.Setenv("INFRAGUARD_WORKSPACE_POLICY_DIR", workspaceDir)

			// Load policies
			loader, err := LoadWithFallback()
			So(err, ShouldBeNil)
			So(loader, ShouldNotBeNil)

			Convey("It should include rules from both sources", func() {
				ruleA := loader.GetRule("rule:aliyun:rule-a")
				So(ruleA, ShouldNotBeNil)
				So(ruleA.Name.Get("en"), ShouldEqual, "Rule A from User")

				ruleB := loader.GetRule("rule:aliyun:rule-b")
				So(ruleB, ShouldNotBeNil)
				So(ruleB.Name.Get("en"), ShouldEqual, "Rule B from Workspace")
			})

			Convey("It should have correct total rule count", func() {
				// Should have rules from both sources (plus any embedded if present)
				rules := loader.GetAllRules()
				So(len(rules), ShouldBeGreaterThanOrEqualTo, 2)
			})
		})
	})
}

func TestManager_Clean(t *testing.T) {
	Convey("Given a Manager instance", t, func() {
		// Create a temporary directory for testing
		tmpDir, err := os.MkdirTemp("", "policy-clean-test")
		So(err, ShouldBeNil)
		defer os.RemoveAll(tmpDir)

		policyDir := filepath.Join(tmpDir, "policies")
		manager := NewManager(policyDir)

		Convey("When cleaning a directory that exists", func() {
			// Create the directory with some files
			err := os.MkdirAll(filepath.Join(policyDir, "subdir"), 0755)
			So(err, ShouldBeNil)
			err = os.WriteFile(filepath.Join(policyDir, "file.txt"), []byte("test"), 0644)
			So(err, ShouldBeNil)

			err = manager.Clean()

			Convey("It should succeed", func() {
				So(err, ShouldBeNil)
			})

			Convey("It should remove the directory", func() {
				_, statErr := os.Stat(policyDir)
				So(os.IsNotExist(statErr), ShouldBeTrue)
			})
		})

		Convey("When cleaning a directory that does not exist", func() {
			// Ensure directory doesn't exist
			os.RemoveAll(policyDir)

			err := manager.Clean()

			Convey("It should succeed without error", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When cleaning fails due to permissions", func() {
			// This test is platform-dependent and may not work on all systems
			// On Unix-like systems, we can create a read-only parent directory
			// Skip on Windows as file permissions work differently there
			if runtime.GOOS == "windows" {
				SkipConvey("Skipping permission test on Windows", func() {})
				return
			}

			// Skip if running as root (permissions won't work as expected)
			if os.Getuid() == 0 {
				SkipConvey("Skipping permission test when running as root", func() {})
				return
			}

			readOnlyParent := filepath.Join(tmpDir, "readonly")
			err := os.MkdirAll(readOnlyParent, 0755)
			So(err, ShouldBeNil)

			protectedDir := filepath.Join(readOnlyParent, "protected")
			err = os.MkdirAll(protectedDir, 0755)
			So(err, ShouldBeNil)

			// Make parent read-only
			err = os.Chmod(readOnlyParent, 0555)
			So(err, ShouldBeNil)
			defer os.Chmod(readOnlyParent, 0755) // Restore permissions for cleanup

			protectedManager := NewManager(protectedDir)
			err = protectedManager.Clean()

			Convey("It should return an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
