@echo off
rem Betterleaks isolates Git's global/system config by setting these variables
rem to Go's os.DevNull. On native ARM64 Git for Windows (clangarm64), that is
rem NUL, which Git rejects as a config-file path with "Invalid argument".
rem This is a child-process compatibility shim, not a replacement scanner.
rem Clear only the invalid paths, keep system config disabled, and preserve
rem repository-local config before delegating to the real Git executable.
if not defined BOXY_REAL_GIT (
  echo BOXY_REAL_GIT is required by the Betterleaks Git shim 1>&2
  exit /b 127
)
set "GIT_CONFIG_GLOBAL="
set "GIT_CONFIG_SYSTEM="
set "GIT_CONFIG_NOSYSTEM=1"
"%BOXY_REAL_GIT%" %*
exit /b %ERRORLEVEL%
