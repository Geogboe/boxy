@echo off
rem Betterleaks v1.7.4 sets these to NUL on Windows. Git for Windows rejects
rem NUL as a config path, so clear them while preserving repository config.
if not defined BOXY_REAL_GIT (
  echo BOXY_REAL_GIT is required by the Betterleaks Git shim 1>&2
  exit /b 127
)
set "GIT_CONFIG_GLOBAL="
set "GIT_CONFIG_SYSTEM="
set "GIT_CONFIG_NOSYSTEM=1"
"%BOXY_REAL_GIT%" %*
exit /b %ERRORLEVEL%
