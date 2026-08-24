# ADR 0001: Identity and namespace

Status: accepted

Hcorral uses `ai.infrasecture.hcorral.*` labels. A workspace SHA-256 covers the
versioned namespace and physical path; a corral SHA-256 covers its own
namespace, that path, and canonical harness type. Generated names are
`hcorral-<underscore_slug>-<first7_corral_id>` and workspace-private volumes use
the first seven workspace-ID characters. Full hashes alone prove ownership.
An explicit project name intentionally permits multiple same-corral instances.
