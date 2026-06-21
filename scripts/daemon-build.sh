#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
backend_dir="${repo_root}/backend"
build_dir="${XDG_CACHE_HOME:-${HOME}/.cache}/aoagents/agent-orchestrator/bin"
binary_path="${build_dir}/ao"

can_write_dir() {
  local dir="$1"

  mkdir -p "${dir}"
  [[ -d "${dir}" && -w "${dir}" ]]
}

select_install_dir() {
  local gopath
  local existing_path
  local dir
  local candidate
  local -a path_entries
  gopath="$(go env GOPATH)"
  existing_path="$(command -v ao || true)"

  if [[ -n "${existing_path}" && "${existing_path}" = /* ]] && can_write_dir "$(dirname "${existing_path}")"; then
    dirname "${existing_path}"
    return 0
  fi

  local candidates=(
    "${gopath}/bin"
    "/usr/local/bin"
    "/opt/homebrew/bin"
    "${HOME}/.local/bin"
  )

  IFS=':' read -r -a path_entries <<< "${PATH:-}"
  for dir in "${path_entries[@]}"; do
    for candidate in "${candidates[@]}"; do
      if [[ "${dir}" == "${candidate}" ]] && can_write_dir "${dir}"; then
        printf '%s\n' "${dir}"
        return 0
      fi
    done
  done

  for dir in "${path_entries[@]}"; do
    if [[ "${dir}" = /* ]] && can_write_dir "${dir}"; then
      printf '%s\n' "${dir}"
      return 0
    fi
  done

  return 1
}

command -v go >/dev/null

mkdir -p "${build_dir}"
(cd "${backend_dir}" && go build -o "${binary_path}" ./cmd/ao)

if ! install_dir="$(select_install_dir)"; then
  printf 'Could not find a writable directory on PATH for ao\n' >&2
  exit 1
fi
install_path="${install_dir}/ao"

ln -sfn "${binary_path}" "${install_path}"

resolved="$(command -v ao)"
if [[ "$(cd "$(dirname "${resolved}")" && pwd -P)/$(basename "${resolved}")" != "$(cd "$(dirname "${install_path}")" && pwd -P)/$(basename "${install_path}")" ]]; then
  printf 'ao resolves to %s, expected %s\n' "${resolved}" "${install_path}" >&2
  exit 1
fi

printf 'Built %s\n' "${binary_path}"
printf 'Linked %s\n' "${install_path}"
