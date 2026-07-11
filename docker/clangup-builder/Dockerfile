ARG BASE_IMAGE=quay.io/centos/centos:7
ARG BASE_PROFILE=el7
ARG PREFIX=/opt/clangup-builder

# -----------------------------------------------------------------------------
# Pinned upstream releases
# -----------------------------------------------------------------------------

ARG CMAKE_VERSION=4.3.4
ARG CMAKE_SHA256_LINUX_X86_64=ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68
ARG CMAKE_SHA256_LINUX_AARCH64=56a8014a8f28b8ff9cbe2c6fa8beebc028ac5b1987195d122b847fb486dc5282
ARG NINJA_VERSION=1.13.2
ARG NINJA_SHA256=974d6b2f4eeefa25625d34da3cb36bdcebe7fbce40f4c16ac0835fd1c0cbae17
ARG PYTHON_VERSION=3.14.6
ARG PYTHON_SHA256=143b1dddefaec3bd2e21e3b839b34a2b7fb9842272883c576420d605e9f30c63
ARG PERL_VERSION=5.42.2
ARG PERL_SHA256=9384e8deb75b7b1695e5637971b752281aaecd025a3d5d4734d33c1d0adfee47
ARG GIT_VERSION=2.54.0
ARG GIT_SHA256=f689162364c10de79ef89aa8dbf48731eb057e34edbbd20aca510ce0154681a3
ARG TAR_VERSION=1.35
ARG TAR_SHA256=4d62ff37342ec7aed748535323930c7cf94acf71c3591882b26a7ea50f3edc16
ARG XZ_VERSION=5.8.3
ARG XZ_SHA256=fff1ffcf2b0da84d308a14de513a1aa23d4e9aa3464d17e64b9714bfdd0bbfb6
ARG ZLIB_VERSION=1.3.2
ARG ZLIB_SHA256=d7a0654783a4da529d1bb793b7ad9c3318020af77667bcae35f95d0e42a792f3
ARG ZSTD_VERSION=1.5.7
ARG ZSTD_SHA256=eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3
ARG EXPAT_VERSION=2.8.2
ARG EXPAT_SHA256=3ad89b8588e6644bd4e49981480d48b21289eebbcd4f0a1a4afb1c29f99b6ab4
ARG OPENSSL_VERSION=3.5.7
ARG OPENSSL_SHA256=a8c0d28a529ca480f9f36cf5792e2cd21984552a3c8e4aa11a24aa31aeac98e8
ARG NGHTTP2_VERSION=1.69.0
ARG NGHTTP2_SHA256=1fb324b6ec2c56f6bde0658f4139ffd8209fa9e77ce98fd7a5f63af8d0e508ad
ARG CURL_VERSION=8.21.0
ARG CURL_SHA256=aa1b66a70eace83dc624508745646c08ae561de512ab403adffb93ac87fc72e6
ARG CENTOS_VAULT_URL=https://vault.centos.org

# -----------------------------------------------------------------------------
# Common package repositories
# -----------------------------------------------------------------------------

FROM ${BASE_IMAGE} AS repo-base

ARG BASE_PROFILE
ARG CENTOS_VAULT_URL

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

COPY docker/scripts/fix-el-repos.sh /usr/local/bin/fix-el-repos

RUN set -eux; \
    chmod 0755 /usr/local/bin/fix-el-repos; \
    fix-el-repos; \
    if [ "${BASE_PROFILE}" = "el7" ]; then \
      yum install -y centos-release-scl-rh; \
      fix-el-repos; \
    fi

# -----------------------------------------------------------------------------
# Statically linked Ninja bootstrap
# -----------------------------------------------------------------------------

FROM repo-base AS ninja-builder

ARG BASE_PROFILE
ARG NINJA_VERSION
ARG NINJA_SHA256

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
    echo "${NINJA_SHA256}  v${NINJA_VERSION}.tar.gz" | sha256sum -c -; \
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

# -----------------------------------------------------------------------------
# Builder base with the platform GCC toolchain
# -----------------------------------------------------------------------------

FROM repo-base AS builder-base

ARG BASE_PROFILE
ARG PREFIX
ARG CMAKE_VERSION
ARG CMAKE_SHA256_LINUX_X86_64
ARG CMAKE_SHA256_LINUX_AARCH64
ARG PYTHON_VERSION
ARG PYTHON_SHA256
ARG PERL_VERSION
ARG PERL_SHA256
ARG GIT_VERSION
ARG GIT_SHA256
ARG TAR_VERSION
ARG TAR_SHA256
ARG XZ_VERSION
ARG XZ_SHA256
ARG ZLIB_VERSION
ARG ZLIB_SHA256
ARG ZSTD_VERSION
ARG ZSTD_SHA256
ARG EXPAT_VERSION
ARG EXPAT_SHA256
ARG OPENSSL_VERSION
ARG OPENSSL_SHA256
ARG NGHTTP2_VERSION
ARG NGHTTP2_SHA256
ARG CURL_VERSION
ARG CURL_SHA256

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
    case "${BASE_PROFILE}" in \
      el7) \
        "${package_manager}" install -y gcc gcc-c++; \
        ;; \
      el8) \
        "${package_manager}" install -y gcc gcc-c++ libstdc++-static; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac; \
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
      gettext-devel \
      glibc-devel \
      gperf \
      groff \
      libffi-devel \
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
    "${package_manager}" clean all; \
    rm -rf /var/cache/yum /var/cache/dnf; \
    mkdir -p "${PREFIX}"; \
    gcc --version; \
    g++ --version; \
    test -f "$(gcc -print-file-name=libgcc_s.so.1)"; \
    test -f "$(g++ -print-file-name=libstdc++.so.6)"

# -----------------------------------------------------------------------------
# Dependency build stage
# -----------------------------------------------------------------------------

FROM builder-base AS deps-builder

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    "${package_manager}" install -y libtool; \
    "${package_manager}" clean all; \
    rm -rf /var/cache/yum /var/cache/dnf; \
    mkdir -p /tmp/clangup-src

WORKDIR /tmp/clangup-src

COPY --from=ninja-builder /opt/clangup-ninja/bin/ninja ${PREFIX}/bin/ninja

# Compression and XML libraries

RUN set -eux; \
    curl -fsSL -o "zlib-${ZLIB_VERSION}.tar.xz" "https://github.com/madler/zlib/releases/download/v${ZLIB_VERSION}/zlib-${ZLIB_VERSION}.tar.xz"; \
    echo "${ZLIB_SHA256}  zlib-${ZLIB_VERSION}.tar.xz" | sha256sum -c -; \
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
    echo "${ZSTD_SHA256}  zstd-${ZSTD_VERSION}.tar.gz" | sha256sum -c -; \
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
    echo "${XZ_SHA256}  xz-${XZ_VERSION}.tar.xz" | sha256sum -c -; \
    tar -xf "xz-${XZ_VERSION}.tar.xz"; \
    cd "xz-${XZ_VERSION}"; \
    ./configure --prefix="${PREFIX}" --disable-shared --enable-static; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "xz-${XZ_VERSION}" "xz-${XZ_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "expat-${EXPAT_VERSION}.tar.xz" "https://github.com/libexpat/libexpat/releases/download/R_${EXPAT_VERSION//./_}/expat-${EXPAT_VERSION}.tar.xz"; \
    echo "${EXPAT_SHA256}  expat-${EXPAT_VERSION}.tar.xz" | sha256sum -c -; \
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

# TLS and HTTP stack

RUN set -eux; \
    curl -fsSL -o "openssl-${OPENSSL_VERSION}.tar.gz" "https://github.com/openssl/openssl/releases/download/openssl-${OPENSSL_VERSION}/openssl-${OPENSSL_VERSION}.tar.gz"; \
    echo "${OPENSSL_SHA256}  openssl-${OPENSSL_VERSION}.tar.gz" | sha256sum -c -; \
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
    echo "${NGHTTP2_SHA256}  nghttp2-${NGHTTP2_VERSION}.tar.xz" | sha256sum -c -; \
    tar -xf "nghttp2-${NGHTTP2_VERSION}.tar.xz"; \
    cd "nghttp2-${NGHTTP2_VERSION}"; \
    ./configure \
      --prefix="${PREFIX}" \
      --disable-shared \
      --enable-static \
      --disable-app \
      --disable-examples; \
    make; \
    make install; \
    rm -f "${PREFIX}/lib/libnghttp2.so" "${PREFIX}/lib/libnghttp2.so."*; \
    cd /tmp/clangup-src; \
    rm -rf "nghttp2-${NGHTTP2_VERSION}" "nghttp2-${NGHTTP2_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "curl-${CURL_VERSION}.tar.xz" "https://github.com/curl/curl/releases/download/curl-${CURL_VERSION//./_}/curl-${CURL_VERSION}.tar.xz"; \
    echo "${CURL_SHA256}  curl-${CURL_VERSION}.tar.xz" | sha256sum -c -; \
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

# Build and scripting tools

RUN set -eux; \
    curl -fsSL -o "tar-${TAR_VERSION}.tar.xz" "https://ftp.gnu.org/gnu/tar/tar-${TAR_VERSION}.tar.xz"; \
    echo "${TAR_SHA256}  tar-${TAR_VERSION}.tar.xz" | sha256sum -c -; \
    tar -xf "tar-${TAR_VERSION}.tar.xz"; \
    cd "tar-${TAR_VERSION}"; \
    FORCE_UNSAFE_CONFIGURE=1 ./configure --prefix="${PREFIX}"; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "tar-${TAR_VERSION}" "tar-${TAR_VERSION}.tar.xz"

RUN set -eux; \
    curl -fsSL -o "perl-${PERL_VERSION}.tar.gz" "https://www.cpan.org/src/5.0/perl-${PERL_VERSION}.tar.gz"; \
    echo "${PERL_SHA256}  perl-${PERL_VERSION}.tar.gz" | sha256sum -c -; \
    tar -xf "perl-${PERL_VERSION}.tar.gz"; \
    cd "perl-${PERL_VERSION}"; \
    ./Configure -des -Dprefix="${PREFIX}" -Dusethreads -Duseshrplib=false; \
    make; \
    make install; \
    cd /tmp/clangup-src; \
    rm -rf "perl-${PERL_VERSION}" "perl-${PERL_VERSION}.tar.gz"

RUN set -eux; \
    curl -fsSL -o "Python-${PYTHON_VERSION}.tar.xz" "https://www.python.org/ftp/python/${PYTHON_VERSION}/Python-${PYTHON_VERSION}.tar.xz"; \
    echo "${PYTHON_SHA256}  Python-${PYTHON_VERSION}.tar.xz" | sha256sum -c -; \
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
      x86_64) cmake_binary_platform=linux-x86_64; cmake_sha256="${CMAKE_SHA256_LINUX_X86_64}" ;; \
      aarch64) cmake_binary_platform=linux-aarch64; cmake_sha256="${CMAKE_SHA256_LINUX_AARCH64}" ;; \
      *) echo "unsupported CMake binary arch: $(uname -m)" >&2; exit 2 ;; \
    esac; \
    curl -fsSL -o "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz" "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"; \
    echo "${cmake_sha256}  cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz" | sha256sum -c -; \
    tar -xf "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"; \
    cp -a "cmake-${CMAKE_VERSION}-${cmake_binary_platform}"/. "${PREFIX}/"; \
    cd /tmp/clangup-src; \
    rm -rf "cmake-${CMAKE_VERSION}-${cmake_binary_platform}" "cmake-${CMAKE_VERSION}-${cmake_binary_platform}.tar.gz"

# The OPENSSL_SHA256 build arg (tarball checksum) collides with a knob of the
# same name in git's Makefile ("use OpenSSL's SHA-256"); leaving it in the
# environment makes git compile the EVP backend while NO_OPENSSL drops
# -lcrypto from the link, so unset it before building.
RUN set -eux; \
    unset OPENSSL_SHA256; \
    curl -fsSL -o "git-${GIT_VERSION}.tar.xz" "https://www.kernel.org/pub/software/scm/git/git-${GIT_VERSION}.tar.xz"; \
    echo "${GIT_SHA256}  git-${GIT_VERSION}.tar.xz" | sha256sum -c -; \
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

# Runtime environment and smoke tests

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

# -----------------------------------------------------------------------------
# Final builder image
# -----------------------------------------------------------------------------

FROM builder-base AS final

ARG PREFIX

COPY --from=deps-builder ${PREFIX}/ ${PREFIX}/

WORKDIR /work
CMD ["/bin/bash", "-l"]
