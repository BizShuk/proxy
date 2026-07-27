---
name: imagine
description: Use when a user asks Codex or Claude Code to create, generate, draw, illustrate, or render a new image through the configured proxy.
---

# Imagine

Call `generate_image` once.

- Use `$ARGUMENTS` verbatim as `prompt` when it is non-empty; otherwise use the user's complete image request.
- Pass `model`, `n`, `aspect_ratio`, or `resolution` only when the user explicitly provides them.
- Return the generated image content and every saved path from the tool result.
