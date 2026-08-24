# Project Instructions

## Shared Development Workspace

- EnvVault is developed across two repositories, and both are part of the default development workspace:
  - Backend: `/Users/vincent/GolandProjects/env-vault`
  - Frontend: `/Users/vincent/Desktop/codes.nosync/env-vault-web`
- Treat `env-vault` as the Go backend and `env-vault-web` as the Vue frontend.
- For every requirement, first determine whether it affects the backend, frontend, or both. API contract or user-flow changes must be checked across both repositories.
- Follow each repository's local instructions and run the relevant validation in every repository changed.
- Do not assume the repositories share the same parent directory; use the paths above when moving between them.

## ApiPost Synchronization

- The ApiPost MCP connection is configured in `.vscode/mcp.json`. Never print, copy, or commit its API token.
- All API definitions for this repository belong under the ApiPost root directory `env_vault`.
- When the user says "将 xxxx 接口同步到 apipost" (or equivalent), locate the existing resource subdirectory under `env_vault` and create or update the API there. Examples include `organization`, `project`, `env`, `folder`, `secrets`, and `tenant`.
- Do not place this project's APIs at the ApiPost project root or outside `env_vault` unless the user explicitly requests it.
- Query the existing ApiPost directory and APIs before writing. If an API with the same method and path already exists, update it instead of creating a duplicate.
- Follow the conventions already used by sibling APIs in the selected resource directory, including server variables, inherited authentication, request headers, naming, and ordering.
- Synchronize the complete contract: method, URL, description, request parameters/body, authentication requirements, success and error examples, and response JSON Schema.
- After writing, fetch the target details again to verify that the change was persisted.
