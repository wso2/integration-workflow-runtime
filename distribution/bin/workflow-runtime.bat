@echo off
rem Starts the Workflow Runtime from conf\runtime.toml. Other workflow-engine commands pass through:
rem   workflow-runtime.bat [start] [--allow-no-auth] ^| render-config ^| version
rem WORKFLOW_RUNTIME_CONFIG names another runtime.toml. Relative paths in it resolve against the runtimes home.
setlocal
set "HOME_DIR=%~dp0.."
set "ENGINE=%HOME_DIR%\bin\workflow-engine.exe"
if not defined WORKFLOW_RUNTIME_CONFIG set "WORKFLOW_RUNTIME_CONFIG=%HOME_DIR%\conf\runtime.toml"
cd /d "%HOME_DIR%"
set "COMMAND=%~1"
if "%COMMAND%"=="" set "COMMAND=start"
if not "%~1"=="" shift
set "REST="
:collect
if "%~1"=="" goto run
set "REST=%REST% %1"
shift
goto collect
:run
if /i "%COMMAND%"=="start" goto withconfig
if /i "%COMMAND%"=="render-config" goto withconfig
"%ENGINE%" %COMMAND%%REST%
exit /b %ERRORLEVEL%
:withconfig
"%ENGINE%" %COMMAND% --runtime-config "%WORKFLOW_RUNTIME_CONFIG%"%REST%
exit /b %ERRORLEVEL%
