# ADR 0002: No myCodex compatibility

Status: accepted

Hcorral does not adopt, migrate, or operate myCodex resources. A narrow
read-only detector blocks a verified same-workspace myCodex container and gives
instructions to use the original launcher. This keeps the new runtime contract
small and prevents two stacks from writing one workspace.
