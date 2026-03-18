#!/usr/bin/env bash
set -euo pipefail

# Usage: ./install-skills.sh <owner/repo> <ref>
# Example: ./install-skills.sh anthropics/skills a3f2c1d
#          ./install-skills.sh anthropics/skills main

REPO="${1:-}"
REF="${2:-}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS_DIR="${SCRIPT_DIR}/skills"
MANIFEST_FILE="${SCRIPT_DIR}/skills-manifest.json"

usage() {
  echo "Usage: $0 <owner/repo> <ref>"
  echo "  owner/repo  GitHub repository (e.g. anthropics/skills)"
  echo "  ref         Commit hash, branch, or tag (e.g. main, v1.0.0, a3f2c1d)"
  exit 1
}

# --- Validate args ---
[[ -z "$REPO" || -z "$REF" ]] && usage

if [[ ! "$REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "Error: repo must be in owner/repo format" >&2
  exit 1
fi

# --- Check deps ---
for cmd in curl unzip; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: '$cmd' is required but not installed." >&2
    exit 1
  fi
done

# --- Setup ---
ZIP_URL="https://github.com/${REPO}/archive/${REF}.zip"
TMP_DIR="$(mktemp -d)"
ZIP_FILE="${TMP_DIR}/skill.zip"
EXTRACT_DIR="${TMP_DIR}/extracted"

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

echo "Fetching ${REPO}@${REF} ..."

# --- Download ---
HTTP_STATUS=$(curl -L \
  --silent --show-error \
  --write-out "%{http_code}" \
  --output "$ZIP_FILE" \
  ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
  "$ZIP_URL")

if [[ "$HTTP_STATUS" != "200" ]]; then
  echo "Error: GitHub returned HTTP $HTTP_STATUS for $ZIP_URL" >&2
  [[ "$HTTP_STATUS" == "404" ]] && echo "Check that the repo and ref exist and are accessible." >&2
  [[ "$HTTP_STATUS" == "401" || "$HTTP_STATUS" == "403" ]] && \
    echo "Private repo? Set GITHUB_TOKEN env var." >&2
  exit 1
fi

# --- Extract ---
mkdir -p "$EXTRACT_DIR"
unzip -q "$ZIP_FILE" -d "$EXTRACT_DIR"

# GitHub zips extract into a single top-level directory named <repo>-<ref>/
EXTRACTED_ROOT="$(find "$EXTRACT_DIR" -mindepth 1 -maxdepth 1 -type d | head -1)"
if [[ -z "$EXTRACTED_ROOT" ]]; then
  echo "Error: zip appears to be empty." >&2
  exit 1
fi

# --- Helper: update manifest ---
# Reads existing manifest (if any), removes prior entries from the same repo,
# and appends the newly installed skills.
update_manifest() {
  local repo="$1"
  local ref="$2"
  shift 2
  local skills=("$@")
  local timestamp
  timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  # Build the JSON array of skill names
  local skills_json="["
  local first=true
  for s in "${skills[@]}"; do
    if $first; then first=false; else skills_json+=","; fi
    skills_json+="\"${s}\""
  done
  skills_json+="]"

  local new_entry
  new_entry=$(cat <<ENTRY_EOF
{"repo":"${repo}","ref":"${ref}","installed_at":"${timestamp}","skills":${skills_json}}
ENTRY_EOF
  )

  if [[ -f "$MANIFEST_FILE" ]]; then
    # Read existing manifest, filter out entries from the same repo, append new
    local existing
    existing="$(cat "$MANIFEST_FILE")"
    # Use a simple approach: rebuild the array
    # Pull out entries that are NOT from this repo, then append
    local filtered
    if command -v python3 &>/dev/null; then
      filtered="$(python3 -c "
import json, sys
data = json.loads(sys.stdin.read())
filtered = [e for e in data if e.get('repo') != '${repo}']
filtered.append(json.loads('${new_entry}'))
json.dump(filtered, sys.stdout, indent=2)
" <<< "$existing")"
    else
      # Fallback: just overwrite with single entry if python3 unavailable
      filtered="[${new_entry}]"
    fi
    echo "$filtered" > "$MANIFEST_FILE"
  else
    # Create new manifest
    if command -v python3 &>/dev/null; then
      python3 -c "
import json, sys
entry = json.loads(sys.stdin.read())
json.dump([entry], sys.stdout, indent=2)
" <<< "$new_entry" > "$MANIFEST_FILE"
    else
      echo "[${new_entry}]" > "$MANIFEST_FILE"
    fi
  fi
}

# --- Find skills to install ---
# Look for skill directories (containing a SKILL.md) within a skills/ subdir,
# or fall back to any SKILL.md at the repo root.
SKILLS_FOUND=()

if [[ -d "${EXTRACTED_ROOT}/skills" ]]; then
  while IFS= read -r skill_md; do
    SKILLS_FOUND+=("$(dirname "$skill_md")")
  done < <(find "${EXTRACTED_ROOT}/skills" -mindepth 2 -maxdepth 2 -name "SKILL.md")
fi

# Skills as top-level subdirectories of the repo (e.g. antithesishq/antithesis-skills)
while IFS= read -r skill_md; do
  SKILLS_FOUND+=("$(dirname "$skill_md")")
done < <(find "$EXTRACTED_ROOT" -mindepth 2 -maxdepth 2 -name "SKILL.md")

# Single skill at the repo root
if [[ -f "${EXTRACTED_ROOT}/SKILL.md" ]]; then
  SKILLS_FOUND=("$EXTRACTED_ROOT" "${SKILLS_FOUND[@]}")
fi

# Deduplicate
IFS=$'\n' read -r -d '' -a SKILLS_FOUND < <(printf '%s\n' "${SKILLS_FOUND[@]}" | sort -u && printf '\0') || true

if [[ ${#SKILLS_FOUND[@]} -eq 0 ]]; then
  echo "Warning: no SKILL.md files found. Copying entire repo contents into ${SKILLS_DIR}/." >&2
  REPO_NAME="${REPO#*/}"
  mkdir -p "${SKILLS_DIR}/${REPO_NAME}"
  cp -r "${EXTRACTED_ROOT}/." "${SKILLS_DIR}/${REPO_NAME}/"
  echo "Installed to ${SKILLS_DIR}/${REPO_NAME}/"
  update_manifest "$REPO" "$REF" "$REPO_NAME"
  echo "Manifest updated: ${MANIFEST_FILE}"
  exit 0
fi

# --- Install each skill ---
mkdir -p "$SKILLS_DIR"
INSTALLED=()

for skill_dir in "${SKILLS_FOUND[@]}"; do
  skill_name="$(basename "$skill_dir")"
  dest="${SKILLS_DIR}/${skill_name}"

  if [[ -d "$dest" ]]; then
    echo "Warning: '${skill_name}' already exists at ${dest} — overwriting."
    rm -rf "$dest"
  fi

  cp -r "$skill_dir" "$dest"
  INSTALLED+=("$skill_name")
done

echo ""
echo "Installed ${#INSTALLED[@]} skill(s) into ${SKILLS_DIR}/:"
for s in "${INSTALLED[@]}"; do
  echo "  ✓ $s"
done

# --- Update manifest ---
update_manifest "$REPO" "$REF" "${INSTALLED[@]}"
echo "Manifest updated: ${MANIFEST_FILE}"
