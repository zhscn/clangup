ARG BASE_IMAGE=quay.io/centos/centos:7
ARG BASE_PROFILE=el7
ARG PREFIX=/opt/clangup-builder
ARG CMAKE_VERSION=3.31.12
ARG NINJA_VERSION=1.13.2
ARG PYTHON_VERSION=3.14.5
ARG PERL_VERSION=5.42.2
ARG GIT_VERSION=2.54.0
ARG TAR_VERSION=1.35
ARG XZ_VERSION=5.8.3
ARG ZLIB_VERSION=1.3.2
ARG ZSTD_VERSION=1.5.7
ARG EXPAT_VERSION=2.8.0
ARG OPENSSL_VERSION=3.5.6
ARG NGHTTP2_VERSION=1.69.0
ARG CURL_VERSION=8.20.0
ARG CENTOS_VAULT_URL=http://archive.kernel.org/centos-vault

FROM ${BASE_IMAGE} AS repo-base

ARG BASE_PROFILE
ARG CENTOS_VAULT_URL

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    case "${BASE_PROFILE}" in \
      el7) \
        sed -i 's|^mirrorlist=|#mirrorlist=|g' /etc/yum.repos.d/*.repo; \
        sed -i "s|^#baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^baseurl=http://vault.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        "${package_manager}" install -y centos-release-scl-rh; \
        sed -i 's|^mirrorlist=|#mirrorlist=|g' /etc/yum.repos.d/*.repo; \
        sed -i "s|^#baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^baseurl=http://mirror.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^baseurl=http://vault.centos.org/centos/[$]releasever|baseurl=${CENTOS_VAULT_URL}/7.9.2009|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^#baseurl=http://mirror.centos.org/centos/7/sclo|baseurl=${CENTOS_VAULT_URL}/centos/7/sclo|g" /etc/yum.repos.d/*.repo; \
        sed -i "s|^baseurl=http://mirror.centos.org/centos/7/sclo|baseurl=${CENTOS_VAULT_URL}/centos/7/sclo|g" /etc/yum.repos.d/*.repo; \
        ;; \
      el8) \
        if grep -Rqs 'mirrorlist.centos.org' /etc/yum.repos.d; then \
          sed -i 's|^mirrorlist=|#mirrorlist=|g' /etc/yum.repos.d/*.repo; \
          sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/*.repo; \
          sed -i 's|^baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/*.repo; \
        fi; \
        sed -i '/^\[powertools\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo; \
        sed -i '/^\[PowerTools\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo; \
        sed -i '/^\[devel\]/,/^\[/ s|^enabled=.*|enabled=1|I' /etc/yum.repos.d/*.repo; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac

FROM repo-base AS ninja-builder

ARG BASE_PROFILE
ARG NINJA_VERSION

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    case "${BASE_PROFILE}" in \
      el7) \
        "${package_manager}" install -y ca-certificates curl devtoolset-11-gcc-c++ devtoolset-11-make gzip python3 tar; \
        export PATH=/opt/rh/devtoolset-11/root/usr/bin:$PATH; \
        ;; \
      el8) \
        "${package_manager}" install -y ca-certificates curl gcc gcc-c++ gzip libstdc++-static make python3 tar; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac; \
    mkdir -p /tmp/clangup-ninja-src /opt/clangup-ninja/bin; \
    cd /tmp/clangup-ninja-src; \
    curl -fsSL -o "v${NINJA_VERSION}.tar.gz" "https://github.com/ninja-build/ninja/archive/refs/tags/v${NINJA_VERSION}.tar.gz"; \
    tar -xf "v${NINJA_VERSION}.tar.gz"; \
    cd "ninja-${NINJA_VERSION}"; \
    CXXFLAGS="-O2 -fPIC" LDFLAGS="-static-libstdc++ -static-libgcc" python3 configure.py --bootstrap; \
    install -m 0755 ninja /opt/clangup-ninja/bin/ninja; \
    /opt/clangup-ninja/bin/ninja --version; \
    ldd /opt/clangup-ninja/bin/ninja | tee /tmp/ninja.ldd; \
    if grep -E 'libstdc[+][+]|libgcc_s' /tmp/ninja.ldd; then \
      echo "ninja has forbidden dynamic C++ runtime dependency" >&2; \
      exit 1; \
    fi

FROM repo-base

ARG BASE_PROFILE
ARG PREFIX
ARG CMAKE_VERSION
ARG PYTHON_VERSION
ARG PERL_VERSION
ARG GIT_VERSION
ARG TAR_VERSION
ARG XZ_VERSION
ARG ZLIB_VERSION
ARG ZSTD_VERSION
ARG EXPAT_VERSION
ARG OPENSSL_VERSION
ARG NGHTTP2_VERSION
ARG CURL_VERSION

ENV PREFIX=${PREFIX}
ENV PATH=${PREFIX}/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=${PREFIX}/lib64:${PREFIX}/lib
ENV PKG_CONFIG_PATH=${PREFIX}/lib64/pkgconfig:${PREFIX}/lib/pkgconfig
ENV CPPFLAGS="-I${PREFIX}/include"
ENV CFLAGS="-O2 -fPIC -I${PREFIX}/include"
ENV CXXFLAGS="-O2 -fPIC -I${PREFIX}/include"
ENV LDFLAGS="-L${PREFIX}/lib64 -L${PREFIX}/lib"
ENV MAKEFLAGS="-j4"

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    "${package_manager}" install -y \
      autoconf \
      automake \
      bison \
      bzip2 \
      bzip2-devel \
      ca-certificates \
      curl \
      curl-devel \
      diffutils \
      expat-devel \
      file \
      findutils \
      gcc \
      gcc-c++ \
      gettext-devel \
      gperf \
      groff \
      libffi-devel \
      libtool \
      make \
      ncurses-devel \
      openssl-devel \
      patch \
      perl-core \
      pkgconfig \
      readline-devel \
      sqlite-devel \
      texinfo \
      unzip \
      which; \
    if [ "${BASE_PROFILE}" = "el8" ]; then \
      "${package_manager}" install -y libstdc++-static; \
    fi; \
    "${package_manager}" clean all; \
    rm -rf /var/cache/yum /var/cache/dnf; \
    mkdir -p "${PREFIX}" /tmp/clangup-src

WORKDIR /tmp/clangup-src

COPY --from=ninja-builder /opt/clangup-ninja/bin/ninja ${PREFIX}/bin/ninja

RUN set -eux; \
    curl -fsSL -o "zlib-${ZLIB_VERSION}.tar.xz" "https://github.com/madler/zlib/releases/download/v${ZLIB_VERSION}/zlib-${ZLIB_VERSION}.tar.xz"; \
    tar -xf "zlib-${ZLIB_VERSION}.tar.xz"; \
    cd "zlib-${ZLIB_VERSION}"; \
    ./configure --prefix="${PREFIX}" --static; \
    make; \
    make install; \
    rm -f "${PREFIX}/lib/libz.so" "${PREFIX}/lib/libz.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "zlib-${ZLIB_VERSION}" "zlib-${ZLIB_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "zstd-${ZSTD_VERSION}.tar.gz" "https://github.com/facebook/zstd/releases/download/v${ZSTD_VERSION}/zstd-${ZSTD_VERSION}.tar.gz"; \
    tar -xf "zstd-${ZSTD_VERSION}.tar.gz"; \
    cd "zstd-${ZSTD_VERSION}"; \
    make -C lib libzstd.a; \
    make -C programs zstd-release HAVE_ZLIB=1 ZLIBDIR="${PREFIX}" LDFLAGS="-L${PREFIX}/lib -pthread" LDLIBS="-Wl,-Bstatic -lz -Wl,-Bdynamic -pthread"; \
    make -C lib install-static install-includes PREFIX="${PREFIX}"; \
    mkdir -p "${PREFIX}/bin"; \
    find programs/obj -type f -name zstd -perm -111 -exec install -m 0755 {} "${PREFIX}/bin/zstd" \; -quit; \
    ln -sf zstd "${PREFIX}/bin/unzstd"; \
    ln -sf zstd "${PREFIX}/bin/zstdcat"; \
    rm -f "${PREFIX}/lib/libzstd.so" "${PREFIX}/lib/libzstd.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "zstd-${ZSTD_VERSION}" "zstd-${ZSTD_VERSION}.tar.gz"

RUN set -eux; \
    curl -fsSL -o "xz-${XZ_VERSION}.tar.xz" "https://github.com/tukaani-project/xz/releases/download/v${XZ_VERSION}/xz-${XZ_VERSION}.tar.xz"; \
    tar -xf "xz-${XZ_VERSION}.tar.xz"; \
    cd "xz-${XZ_VERSION}"; \
    ./configure --prefix="${PREFIX}" --disable-shared --enable-static; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "xz-${XZ_VERSION}" "xz-${XZ_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "expat-${EXPAT_VERSION}.tar.xz" "https://github.com/libexpat/libexpat/releases/download/R_${EXPAT_VERSION//./_}/expat-${EXPAT_VERSION}.tar.xz"; \
    tar -xf "expat-${EXPAT_VERSION}.tar.xz"; \
    cd "expat-${EXPAT_VERSION}"; \
    ./configure \
      --prefix="${PREFIX}" \
      --disable-shared \
      --enable-static \
      --without-docbook; \
    make; \
    make install; \
    rm -f "${PREFIX}/lib/libexpat.so" "${PREFIX}/lib/libexpat.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "expat-${EXPAT_VERSION}" "expat-${EXPAT_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "openssl-${OPENSSL_VERSION}.tar.gz" "https://github.com/openssl/openssl/releases/download/openssl-${OPENSSL_VERSION}/openssl-${OPENSSL_VERSION}.tar.gz"; \
    tar -xf "openssl-${OPENSSL_VERSION}.tar.gz"; \
    cd "openssl-${OPENSSL_VERSION}"; \
    case "$(uname -m)" in \
      x86_64) openssl_target=linux-x86_64 ;; \
      aarch64) openssl_target=linux-aarch64 ;; \
      *) echo "unsupported OpenSSL build arch: $(uname -m)" >&2; exit 2 ;; \
    esac; \
    ./Configure "${openssl_target}" \
      --prefix="${PREFIX}" \
      --openssldir="${PREFIX}/ssl" \
      no-shared \
      no-tests \
      no-zlib \
      -I"${PREFIX}/include" \
      -L"${PREFIX}/lib"; \
    make; \
    make install_sw; \
    cd /tmp/clangup-src; \
    rm -rf "openssl-${OPENSSL_VERSION}" "openssl-${OPENSSL_VERSION}.tar.gz"

RUN set -eux; \
    curl -fsSL -o "nghttp2-${NGHTTP2_VERSION}.tar.xz" "https://github.com/nghttp2/nghttp2/releases/download/v${NGHTTP2_VERSION}/nghttp2-${NGHTTP2_VERSION}.tar.xz"; \
    tar -xf "nghttp2-${NGHTTP2_VERSION}.tar.xz"; \
    cd "nghttp2-${NGHTTP2_VERSION}"; \
    ./configure \
      --prefix="${PREFIX}" \
      --disable-shared \
      --enable-static \
      --disable-app \
      --disable-examples \
      --disable-python-bindings; \
    make; \
    make install; \
    rm -f "${PREFIX}/lib/libnghttp2.so" "${PREFIX}/lib/libnghttp2.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "nghttp2-${NGHTTP2_VERSION}" "nghttp2-${NGHTTP2_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "curl-${CURL_VERSION}.tar.xz" "https://github.com/curl/curl/releases/download/curl-${CURL_VERSION//./_}/curl-${CURL_VERSION}.tar.xz"; \
    tar -xf "curl-${CURL_VERSION}.tar.xz"; \
    cd "curl-${CURL_VERSION}"; \
    ./configure \
      --prefix="${PREFIX}" \
      --disable-shared \
      --enable-static \
      --with-openssl="${PREFIX}" \
      --with-nghttp2="${PREFIX}" \
      --with-zlib="${PREFIX}" \
      --with-zstd="${PREFIX}" \
      --without-brotli \
      --without-libidn2 \
      --without-libpsl \
      --without-librtmp \
      --without-libssh2 \
      --without-nghttp3 \
      --without-ngtcp2 \
      --without-quiche \
      --disable-ldap \
      --disable-ldaps; \
    make; \
    make install; \
    rm -f "${PREFIX}/lib/libcurl.so" "${PREFIX}/lib/libcurl.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "curl-${CURL_VERSION}" "curl-${CURL_VERSION}.tar.xz"; \
    hash -r; \
    curl --version

RUN set -eux; \
    curl -fsSL -o "tar-${TAR_VERSION}.tar.xz" "http://ftp.gnu.org/gnu/tar/tar-${TAR_VERSION}.tar.xz"; \
    tar -xf "tar-${TAR_VERSION}.tar.xz"; \
    cd "tar-${TAR_VERSION}"; \
    FORCE_UNSAFE_CONFIGURE=1 ./configure --prefix="${PREFIX}"; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "tar-${TAR_VERSION}" "tar-${TAR_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "perl-${PERL_VERSION}.tar.gz" "http://www.cpan.org/src/5.0/perl-${PERL_VERSION}.tar.gz"; \
    tar -xf "perl-${PERL_VERSION}.tar.gz"; \
    cd "perl-${PERL_VERSION}"; \
    ./Configure -des -Dprefix="${PREFIX}" -Dusethreads -Duseshrplib=false; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "perl-${PERL_VERSION}" "perl-${PERL_VERSION}.tar.gz"

RUN set -eux; \
    curl -fsSL -o "Python-${PYTHON_VERSION}.tar.xz" "https://www.python.org/ftp/python/${PYTHON_VERSION}/Python-${PYTHON_VERSION}.tar.xz"; \
    tar -xf "Python-${PYTHON_VERSION}.tar.xz"; \
    cd "Python-${PYTHON_VERSION}"; \
    LIBS="-lz -ldl -pthread" ./configure \
      --prefix="${PREFIX}" \
      --enable-shared \
      --with-system-expat \
      --with-openssl="${PREFIX}" \
      --with-openssl-rpath=auto \
      --with-ensurepip=install; \
    make; \
    make install; \
    ln -sf "${PREFIX}/bin/python3" "${PREFIX}/bin/python"; \
    cd /tmp/clangup-src; \
    rm -rf "Python-${PYTHON_VERSION}" "Python-${PYTHON_VERSION}.tar.xz"

RUN set -eux; \
    case "$(uname -m)" in \
      x86_64) cmake_binary_platform=linux-x86_64 ;; \
      aarch64) cmake_binary_platform=linux-aarch64 ;; \
      *) echo "unsupported CMake binary arch: $(uname -m)" >&2; exit 2 ;; \
    esac; \
    curl -fsSL -o "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz" "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"; \
    tar -xf "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"; \
    cp -a "cmake-${CMAKE_VERSION}-${cmake_binary_platform}"/. "${PREFIX}/"; \
    cd /tmp/clangup-src; \
    rm -rf "cmake-${CMAKE_VERSION}-${cmake_binary_platform}" "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"

RUN set -eux; \
    curl -fsSL -o "git-${GIT_VERSION}.tar.xz" "https://www.kernel.org/pub/software/scm/git/git-${GIT_VERSION}.tar.xz"; \
    tar -xf "git-${GIT_VERSION}.tar.xz"; \
    cd "git-${GIT_VERSION}"; \
    make configure; \
    git_curl_ldflags="$("${PREFIX}/bin/curl-config" --static-libs)"; \
    git_curl_probe_libs="${git_curl_ldflags#${PREFIX}/lib/libcurl.a }"; \
    CPPFLAGS="-I${PREFIX}/include" \
    LDFLAGS="-L${PREFIX}/lib64 -L${PREFIX}/lib" \
    LIBS="${git_curl_probe_libs}" \
    CURL_CONFIG="${PREFIX}/bin/curl-config" \
    CURL_LDFLAGS="${git_curl_ldflags}" \
      ./configure --prefix="${PREFIX}" --with-curl --with-expat --without-openssl; \
    make all \
      NO_TCLTK=YesPlease \
      NO_OPENSSL=YesPlease \
      CSPRNG_METHOD=urandom \
      CFLAGS="-std=gnu99 ${CFLAGS}" \
      PTHREAD_LIBS="-lpthread" \
      ZLIB_PATH="${PREFIX}" \
      EXPATDIR="${PREFIX}" \
      CURL_CONFIG="${PREFIX}/bin/curl-config" \
      CURL_LDFLAGS="${git_curl_ldflags}"; \
    make install \
      NO_TCLTK=YesPlease \
      NO_OPENSSL=YesPlease \
      CSPRNG_METHOD=urandom \
      CFLAGS="-std=gnu99 ${CFLAGS}" \
      PTHREAD_LIBS="-lpthread" \
      ZLIB_PATH="${PREFIX}" \
      EXPATDIR="${PREFIX}" \
      CURL_CONFIG="${PREFIX}/bin/curl-config" \
      CURL_LDFLAGS="${git_curl_ldflags}"; \
    cd /tmp/clangup-src; \
    rm -rf "git-${GIT_VERSION}" "git-${GIT_VERSION}.tar.xz"

RUN set -eux; \
    rm -rf /tmp/clangup-src; \
    cat > "${PREFIX}/env" <<EOF
export CLANGUP_BUILDER_PREFIX=${PREFIX}
export PATH=${PREFIX}/bin:\$PATH
export LD_LIBRARY_PATH=${PREFIX}/lib64:${PREFIX}/lib\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}
export PKG_CONFIG_PATH=${PREFIX}/lib64/pkgconfig:${PREFIX}/lib/pkgconfig\${PKG_CONFIG_PATH:+:\$PKG_CONFIG_PATH}
export CMAKE_PREFIX_PATH=${PREFIX}\${CMAKE_PREFIX_PATH:+:\$CMAKE_PREFIX_PATH}
export CPPFLAGS="-I${PREFIX}/include \${CPPFLAGS:-}"
export LDFLAGS="-L${PREFIX}/lib64 -L${PREFIX}/lib \${LDFLAGS:-}"
EOF

RUN set -eux; \
    source "${PREFIX}/env"; \
    curl --version; \
    ldd "${PREFIX}/bin/curl" | tee /tmp/curl.ldd; \
    if grep -E 'lib(z|zstd|ssl|crypto|nghttp2)[.]so' /tmp/curl.ldd; then \
      echo "curl has forbidden dynamic dependency" >&2; \
      exit 1; \
    fi; \
    cmake --version; \
    ninja --version; \
    ldd "${PREFIX}/bin/ninja" | tee /tmp/ninja.ldd; \
    if grep -E 'libstdc[+][+]|libgcc_s' /tmp/ninja.ldd; then \
      echo "ninja has forbidden dynamic C++ runtime dependency" >&2; \
      exit 1; \
    fi; \
    python3 --version; \
    python3 -c 'import ssl, hashlib; print(ssl.OPENSSL_VERSION); print(hashlib.sha256(b"x").hexdigest())'; \
    perl -v | head -2; \
    git --version; \
    tar --version | head -1; \
    xz --version | head -1; \
    zstd --version; \
    test -f "${PREFIX}/lib/libz.a"; \
    test -f "${PREFIX}/lib/libzstd.a"; \
    test -f "${PREFIX}/lib/libexpat.a"; \
    test -f "${PREFIX}/lib/libnghttp2.a"; \
    { test -f "${PREFIX}/lib64/libssl.a" || test -f "${PREFIX}/lib/libssl.a"; }; \
    { test -f "${PREFIX}/lib64/libcrypto.a" || test -f "${PREFIX}/lib/libcrypto.a"; }; \
    ! test -e "${PREFIX}/lib/libz.so"; \
    ! test -e "${PREFIX}/lib/libzstd.so"

WORKDIR /work
CMD ["/bin/bash", "-l"]
