# ADR 0001: Identity and namespace

Status: accepted

Hcorral uses `ai.infrasecture.hcorral.*` labels and a full SHA-256 over the
versioned namespace, NUL separator, and physical workspace path. Generated
names are `hcorral-<underscore_slug>-<7-hex>`; the full hash alone proves
ownership. Short-hash collisions fail closed and require an explicit project
name.
