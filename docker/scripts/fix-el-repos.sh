#!/usr/bin/env bash

set -euo pipefail

BASE_PROFILE="${BASE_PROFILE:?BASE_PROFILE is required}"
CENTOS_VAULT_URL="${CENTOS_VAULT_URL:-https://vault.centos.org}"

case "${BASE_PROFILE}" in
  el7)
    sed -i 's|^mirrorlist=|#mirrorlist=|g' /etc/yum.repos.d/*.repo
    sed -i "s|^#baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo
    sed -i "s|^baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo
    sed -i "s|^baseurl=http://vault.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo
    sed -i "s|^#baseurl=http://mirror.centos.org/centos/7/sclo|baseurl=${CENTOS_VAULT_URL}/centos/7/sclo|g" /etc/yum.repos.d/*.repo
    sed -i "s|^baseurl=http://mirror.centos.org/centos/7/sclo|baseurl=${CENTOS_VAULT_URL}/centos/7/sclo|g" /etc/yum.repos.d/*.repo
    ;;
  el8)
    if grep -Rqs 'mirrorlist.centos.org' /etc/yum.repos.d; then
      sed -i 's|^mirrorlist=|#mirrorlist=|g' /etc/yum.repos.d/*.repo
      sed -i "s|^#baseurl=http://mirror.centos.org|baseurl=${CENTOS_VAULT_URL}|g" /etc/yum.repos.d/*.repo
      sed -i "s|^baseurl=http://mirror.centos.org|baseurl=${CENTOS_VAULT_URL}|g" /etc/yum.repos.d/*.repo
    fi
    sed -i '/^\[powertools\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo
    sed -i '/^\[PowerTools\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo
    sed -i '/^\[devel\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo
    ;;
  *)
    echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2
    exit 2
    ;;
esac
