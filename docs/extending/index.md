# Extending Mekami

Mekami is language-agnostic by design. The Go frontend ships in the box; everything else plugs in through the `api.Frontend` contract.

<ul class="card-list">
  <li>
    <a href="frontend-contract/"><span>:handshake:</span> Frontend contract</a>
    <p>The <code>api.Frontend</code> interface and the data types every frontend must produce.</p>
  </li>
  <li>
    <a href="writing-a-frontend/"><span>:wrench:</span> Writing a frontend</a>
    <p>Step-by-step walkthrough: build a Rust frontend against the <code>api/v1</code> contract.</p>
  </li>
  <li>
    <a href="all-gen/"><span>:gear:</span> all_gen</a>
    <p>How the dev builtin set is generated and how the release set is frozen.</p>
  </li>
</ul>
