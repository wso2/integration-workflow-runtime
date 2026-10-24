# patches

No fork patches are in use. When one is needed (0001, cases 4 and 5), `engine/go.mod` gets a `replace` line
pointing at the fork tag, and this file lists each patch:

| Fork tag | Patch | Upstream PR | Drop when |
|---|---|---|---|
