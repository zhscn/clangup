# Linux runtime sub-build configuration for the default channel.

set(_build "$ENV{CLANGUP_BUILD}")
set(_triple "$ENV{CLANGUP_TARGET_TRIPLE}")

set(_cxx_flags
    "-nostdinc++ -isystem ${_build}/include/${_triple}/c++/v1 -isystem ${_build}/include/c++/v1 -nostdlib++")
set(_linker_flags
    "--rtlib=compiler-rt --unwindlib=none -L${_build}/lib -Wl,--no-as-needed,-l:libgcc_s.so.1,--as-needed")

set(CMAKE_CXX_FLAGS "${_cxx_flags}" CACHE STRING "" FORCE)
set(CMAKE_EXE_LINKER_FLAGS "${_linker_flags}" CACHE STRING "" FORCE)
set(CMAKE_SHARED_LINKER_FLAGS "${_linker_flags}" CACHE STRING "" FORCE)
set(LIBCXX_ADDITIONAL_COMPILE_FLAGS
    "-flto;-ffat-lto-objects" CACHE STRING "" FORCE)
set(LIBCXXABI_ADDITIONAL_COMPILE_FLAGS
    "-flto;-ffat-lto-objects" CACHE STRING "" FORCE)
set(SANITIZER_CXX_ABI libcxxabi CACHE STRING "" FORCE)
set(SANITIZER_USE_STATIC_CXX_ABI ON CACHE BOOL "" FORCE)
