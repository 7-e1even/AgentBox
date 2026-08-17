@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT=%~dp0"
set "APP_DIR=%ROOT%app"
set "SERVER_DIR=%ROOT%server"
set "API_URL=http://127.0.0.1:8091"
set "WEB_URL=http://127.0.0.1:3000"

where go.exe >nul 2>&1 || goto missing_go
where pnpm.cmd >nul 2>&1 || goto missing_pnpm

call :assert_port_free 8091 API || goto failed
call :assert_port_free 3000 Web || goto failed

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
set "AGENTBOX_API_URL=%API_URL%"

echo Starting AgentBox API and Web...
start "AgentBox API" /D "%SERVER_DIR%" cmd.exe /k "set PORT=8091&&go run ./cmd/agentbox"
start "AgentBox Web" /D "%APP_DIR%" cmd.exe /k "set PORT=3000&&pnpm.cmd dev --hostname 127.0.0.1 --port 3000"

set "API_READY="
set "WEB_READY="
for /L %%I in (1,1,120) do (
  if not defined API_READY curl.exe --fail --silent --show-error "%API_URL%/healthz" >nul 2>&1 && set "API_READY=1"
  if not defined WEB_READY curl.exe --fail --silent --show-error "%WEB_URL%/api/auth/status" >nul 2>&1 && set "WEB_READY=1"
  if defined API_READY if defined WEB_READY goto ready
  >nul timeout.exe /t 1 /nobreak
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
start "" "%WEB_URL%"
exit /b 0

:assert_port_free
netstat.exe -ano -p tcp | findstr.exe /C:":%~1" | findstr.exe /C:"LISTENING" >nul 2>&1
if not errorlevel 1 (
  echo %~2 port %~1 is already in use.
  exit /b 1
)
exit /b 0

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
  >nul timeout.exe /t 1 /nobreak
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

:failed
echo.
echo AgentBox was not started.
pause
exit /b 1
