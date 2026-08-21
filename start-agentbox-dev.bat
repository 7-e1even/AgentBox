@echo off
setlocal

set "AGENTBOX_WEB_MODE=development"
call "%~dp0start-agentbox.bat"
exit /b %errorlevel%
