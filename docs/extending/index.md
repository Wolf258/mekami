---
title: Extending Mekami
sidebar_label: Overview
---

# Extending Mekami

Mekami is designed to be extended. The CLI, the daemon, the read-side, and the storage layer are all language-agnostic. Adding support for a new language means implementing the `api.Frontend` interface and registering the indexer.

import CardGrid, { Card } from '@site/src/components/CardGrid';

<CardGrid>
  <Card
    icon="📄"
    title="Frontend contract"
    description="The `api/v1` package: every type and method your indexer must implement."
    to="/extending/frontend-contract"
  />
  <Card
    icon="🔨"
    title="Writing a frontend"
    description="Walkthrough for adding a new language. Uses `mekami-core-rust` as a worked example."
    to="/extending/writing-a-frontend"
  />
  <Card
    icon="⚙️"
    title="The `all_gen` mechanism"
    description="How the dev vs production blank-import flow works and when to regenerate."
    to="/extending/all-gen"
  />
</CardGrid>
