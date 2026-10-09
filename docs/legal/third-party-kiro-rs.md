# Third-Party Attribution: ZyphrZero/kiro.rs

This project incorporates protocol-level designs and adapted implementations derived from:

- **Project**: [ZyphrZero/kiro.rs](https://github.com/ZyphrZero/kiro.rs)
- **Pinned version**: `v0.7.1`
- **License**: MIT License
- **Copyright**: Copyright (c) 2026 hank9999

## Scope used by Sub2API

Only the **Kiro data-plane protocol** behavior is ported/adapted into Go under:

- `backend/internal/pkg/kiro/`
- `backend/internal/pkg/awseventstream/`
- `backend/internal/service/kiro_gateway_*.go`

Not ported:

- Rust Admin UI
- Client-side API keys / SQLite trace
- Online auto-update machinery
- Unrelated product surface from kiro.rs

## MIT License (upstream)

```
MIT License

Copyright (c) 2026 hank9999

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Sub2API remains under its own project license for original code. This notice
satisfies attribution for the adapted Kiro protocol modules fixed at kiro.rs `v0.7.1`.
