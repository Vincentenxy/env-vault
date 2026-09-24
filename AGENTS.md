# Project Instructions

## Shared Development Workspace

- Secret management module:
  - Java SDK and primary working directory (`env-vault-client`): `E:\Java\Projects\env-vault-client`
  - Backend (`env-vault`, Go): `E:\goland\env-vault`
  - Frontend (`env-vault-web`, Vue): `E:\Projects\vue\env-vault-web`
- Publishing module:
  - Backend (`publish-devops-api`): `/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`
  - Frontend (`devops-frontend`): `/Users/vincent/Desktop/codes.nosync/devops-frontend`
- A request to modify the publishing frontend targets `devops-frontend`; a request to modify the secret management frontend targets `env-vault-web`.
- For Env Vault work, use `env-vault-client` as the default coordinating repository unless the request clearly targets the backend or frontend.
- For every Env Vault requirement, first determine whether it affects the Java SDK, Go backend, Vue frontend, or multiple repositories. API contract or user-flow changes must be checked across all affected repositories.
- Follow each repository's local instructions and run the relevant validation in every repository changed.
- Do not assume the repositories share the same parent directory; use the paths above when moving between them.

## Cross-Project Scope Guard

- This conversation and repository default to the Env Vault secret management module. The shared workspace paths identify related repositories; they do not authorize switching to another business module.
- Before editing, identify the module that owns the request. Necessary backend, frontend, and SDK changes for the same Env Vault requirement remain within scope; state which repositories are affected.
- If a request belongs to another module, such as the publishing module, explicitly tell the user the current module, target repository, and proposed changes. Ask whether they intended to switch projects, and wait for explicit confirmation before modifying files or running operations that change that project's state. Read-only inspection to identify ownership is allowed.
- Merely confirming which page the user means is not confirmation to switch projects. Browser context, matching code in another repository, and historical work on that repository do not substitute for switch confirmation. Once the user explicitly confirms the switch, do not ask again for the same task.
- If the user says the request was posted in the wrong conversation, stop that cross-project task. Report any changes already made and leave them intact unless the user requests a rollback.

## ApiPost Synchronization

- The ApiPost MCP connection is configured in `.vscode/mcp.json`. Never print, copy, or commit its API token.
- All API definitions for this repository belong under the ApiPost root directory `env_vault`.
- When the user says "将 xxxx 接口同步到 apipost" (or equivalent), locate the existing resource subdirectory under `env_vault` and create or update the API there. Examples include `organization`, `project`, `env`, `folder`, `secrets`, and `tenant`.
- Do not place this project's APIs at the ApiPost project root or outside `env_vault` unless the user explicitly requests it.
- Query the existing ApiPost directory and APIs before writing. If an API with the same method and path already exists, update it instead of creating a duplicate.
- Follow the conventions already used by sibling APIs in the selected resource directory, including server variables, inherited authentication, request headers, naming, and ordering.
- Synchronize the complete contract: method, URL, description, request parameters/body, authentication requirements, success and error examples, and response JSON Schema.
- After writing, fetch the target details again to verify that the change was persisted.
