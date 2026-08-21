@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT=%~dp0"
set "APP_DIR=%ROOT%app"
set "SERVER_DIR=%ROOT%server"
set "API_URL=http://127.0.0.1:8091"
set "WEB_URL=http://127.0.0.1:3000"
if not defined AGENTBOX_WEB_MODE set "AGENTBOX_WEB_MODE=production"

if /I not "%AGENTBOX_WEB_MODE%"=="production" if /I not "%AGENTBOX_WEB_MODE%"=="development" goto invalid_web_mode

where go.exe >nul 2>&1 || goto missing_go
where pnpm.cmd >nul 2>&1 || goto missing_pnpm

call :free_port 8091 API || goto failed
call :free_port 3000 Web || goto failed
call :clean_web_build || goto failed

if not exist "%APP_DIR%\node_modules" (
  echo Installing web dependencies...
  call pnpm.cmd --dir "%APP_DIR%" install --frozen-lockfile || goto failed
)

set "DATABASE_CONFIGURED="
if defined DATABASE_URL set "DATABASE_CONFIGURED=1"
if not defined DATABASE_CONFIGURED if exist "%SERVER_DIR%\.env" (
  findstr /R /C:"^[ ]*DATABASE_URL[ ]*=" "%SERVER_DIR%\.env" >nul 2>&1 && set "DATABASE_CONFIGURED=1"
)

if not defined DATABASE_CONFIGURED (
  call :start_database
  if errorlevel 1 goto failed
)

if not defined AGENTBOX_ENV set "AGENTBOX_ENV=development"
set "AGENTBOX_BIND_HOST=127.0.0.1"
set "ALLOWED_ORIGINS=http://localhost:3000,http://127.0.0.1:3000"
if exist "%APP_DIR%\.env.local" (
  for /f "usebackq tokens=1,* delims==" %%A in ('findstr.exe /B /C:"AGENTBOX_PUBLIC_URL=" "%APP_DIR%\.env.local"') do set "AGENTBOX_PUBLIC_URL=%%B"
)
if defined AGENTBOX_PUBLIC_URL set "ALLOWED_ORIGINS=!ALLOWED_ORIGINS!,!AGENTBOX_PUBLIC_URL!"
set "AGENTBOX_API_URL=%API_URL%"

if /I "%AGENTBOX_WEB_MODE%"=="production" (
  echo Building AgentBox Web for production...
  call pnpm.cmd --dir "%APP_DIR%" build || goto failed
)

echo Starting AgentBox API and Web ^(%AGENTBOX_WEB_MODE%^)...
start "AgentBox API" /D "%SERVER_DIR%" cmd.exe /c "set PORT=8091&&go run ./cmd/agentbox"
if /I "%AGENTBOX_WEB_MODE%"=="development" (
  start "AgentBox Web" /D "%APP_DIR%" cmd.exe /c "set PORT=3000&&pnpm.cmd dev --hostname 0.0.0.0 --port 3000"
) else (
  start "AgentBox Web" /D "%APP_DIR%" cmd.exe /c "set PORT=3000&&pnpm.cmd start --hostname 0.0.0.0 --port 3000"
)

set "API_READY="
set "WEB_READY="
for /L %%I in (1,1,120) do (
  if not defined API_READY curl.exe --fail --silent --show-error "%API_URL%/healthz" >nul 2>&1 && set "API_READY=1"
  if not defined WEB_READY curl.exe --fail --silent --show-error "%WEB_URL%/api/auth/status" >nul 2>&1 && set "WEB_READY=1"
  if defined API_READY if defined WEB_READY goto ready
  >nul 2>&1 timeout.exe /t 1 /nobreak
)

echo.
echo AgentBox did not become ready within 120 seconds.
echo Check the "AgentBox API" and "AgentBox Web" windows for details.
goto failed

:ready
echo.
echo AgentBox is ready: %WEB_URL%
echo API health: %API_URL%/healthz
echo Close the two AgentBox terminal windows to stop the services.
exit /b 0

:free_port
set "PORT_BUSY="
for /f "tokens=5" %%P in ('netstat.exe -ano -p tcp ^| findstr.exe /R /C:":%~1 .*LISTENING"') do (
  if not defined PORT_BUSY (
    set "PORT_BUSY=1"
    echo %~2 port %~1 is in use, killing the occupying process^(es^)...
  )
  taskkill.exe /PID %%P /T /F >nul 2>&1
)
if defined PORT_BUSY >nul 2>&1 timeout.exe /t 1 /nobreak
exit /b 0

:clean_web_build
for %%I in ("%APP_DIR%") do set "RESOLVED_APP_DIR=%%~fI"
for %%I in ("%APP_DIR%\.next") do set "NEXT_BUILD_DIR=%%~fI"
if /I not "!NEXT_BUILD_DIR!"=="!RESOLVED_APP_DIR!\.next" (
  echo Refusing to clean unexpected Next.js build path: !NEXT_BUILD_DIR!
  exit /b 1
)
if not exist "!NEXT_BUILD_DIR!" exit /b 0

echo Cleaning stale Next.js build artifacts...
for /L %%I in (1,1,10) do (
  rmdir /S /Q "!NEXT_BUILD_DIR!" >nul 2>&1
  if not exist "!NEXT_BUILD_DIR!" exit /b 0
  >nul 2>&1 timeout.exe /t 1 /nobreak
)
echo Failed to clean Next.js build artifacts after 10 attempts: !NEXT_BUILD_DIR!
exit /b 1

:start_database
where docker.exe >nul 2>&1 || goto missing_database
docker.exe info >nul 2>&1 || goto missing_database

docker.exe container inspect agentbox-dev-postgres >nul 2>&1
if errorlevel 1 (
  echo Starting a local PostgreSQL container...
  docker.exe run --detach --name agentbox-dev-postgres --publish 127.0.0.1:54329:5432 --env POSTGRES_DB=agentbox --env POSTGRES_USER=agentbox --env POSTGRES_PASSWORD=agentbox-dev --volume agentbox-dev-postgres:/var/lib/postgresql/data postgres:17-alpine >nul || exit /b 1
) else (
  docker.exe start agentbox-dev-postgres >nul || exit /b 1
)

for /L %%I in (1,1,60) do (
  docker.exe exec agentbox-dev-postgres pg_isready -U agentbox -d agentbox >nul 2>&1 && goto database_ready
  >nul 2>&1 timeout.exe /t 1 /nobreak
)
echo PostgreSQL did not become ready within 60 seconds.
exit /b 1

:database_ready
set "DATABASE_URL=postgresql://agentbox:agentbox-dev@127.0.0.1:54329/agentbox?sslmode=disable"
exit /b 0

:missing_go
echo Go is required. Install Go 1.24 or newer and try again.
goto failed

:missing_pnpm
echo pnpm is required. Install Node.js 20.9 or newer and enable pnpm with Corepack.
goto failed

:missing_database
echo DATABASE_URL is not configured and Docker is not available.
echo Add DATABASE_URL to server\.env or start Docker Desktop.
goto failed

:invalid_web_mode
echo AGENTBOX_WEB_MODE must be production or development.
goto failed

:failed
echo.
echo AgentBox was not started.
pause
exit /b 1
