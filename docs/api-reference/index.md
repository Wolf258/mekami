# API reference

The public surface of Mekami is small. There are two halves:

- The `api/v1` contract every language indexer implements.
- The CLI / MCP / supervisor / daemon (a single Go binary).

The CLI is the user-facing surface; for the `api/v1` contract, see the [Frontend API](frontend-api.md) page.

<ul class="card-list">
  <li>
    <a href="frontend-api/"><span>:electric_plug:</span> Frontend API</a>
    <p><code>api.Frontend</code>, <code>ParseResult</code>, <code>Symbol</code>, <code>Ref</code>, <code>Workspace</code>, <code>ModuleInfo</code>, <code>ModuleEntry</code>, and the <code>Registry</code>.</p>
  </li>
</ul>
