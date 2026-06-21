---
title: API reference
sidebar_label: Overview
---

# API reference

The public surface of Mekami is small. There are two halves:

- The `api/v1` contract every language indexer implements.
- The CLI / MCP / supervisor / daemon (a single Go binary).

The CLI is the user-facing surface; for the `api/v1` contract, see the [Frontend API](frontend-api.md) page.

import CardGrid, { Card } from '@site/src/components/CardGrid';

<CardGrid>
  <Card
    icon="🔌"
    title="Frontend API"
    description="`api.Frontend`, `ParseResult`, `Symbol`, `Ref`, `Workspace`, `ModuleInfo`, `ModuleEntry`, and the `Registry`."
    to="/api-reference/frontend-api"
  />
</CardGrid>
