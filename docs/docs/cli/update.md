---
title: infraguard update
---

# infraguard update

Update InfraGuard CLI to the latest version or a specific version.
The command reads the latest version from the OSS release mirror and downloads the matching platform binary.
If the OSS binary is missing, InfraGuard falls back to the matching GitHub Release asset for compatibility with historical releases.

## Synopsis

```bash
infraguard update [flags]
```

## Flags

| Flag | Type | Description |
|------|------|-------------|
| `--check` | boolean | Check for updates without installing |
| `-f`, `--force` | boolean | Force update even if version is current |
| `--version` | string | Update to a specific version |

## Examples

### Check for Updates

Check if a new version is available without installing:

```bash
infraguard update --check
```

Output:
```
Checking for updates...
Current version: 0.4.0
Latest version: 0.5.0
✓ A new version is available: 0.5.0
```

### Update to Latest Version

Update to the latest available version:

```bash
infraguard update
```

Output:
```
Checking for updates...
Current version: 0.4.0
Latest version: 0.5.0
→ Downloading version 0.5.0...
Downloaded 39.5 MiB / 39.5 MiB (100.0%)
✓ Successfully updated to version 0.5.0!
```

### Update to Specific Version

Install a specific version:

```bash
infraguard update --version 0.5.0
```

This downloads:

```text
https://ros-public-tools.oss-cn-beijing.aliyuncs.com/github-releases/aliyun/infraguard/0.5.0/infraguard-v0.5.0-<os>-<arch>
```

If that OSS object does not exist, InfraGuard tries compatible legacy OSS names, then searches the GitHub Release for the same version and accepts historical raw binary asset names such as `infraguard-v0.5.0-linux-amd64`.

### Force Reinstall Current Version

Reinstall the current version:

```bash
infraguard update --force
# or
infraguard update -f
```
