---
trigger: always_on
---

# Rules for this Workspace

- **Language & Style**: For backend functionality, use Golang. For Front-end, use modern web technologies including React and Typescript.
- **Documentation**: Every function must have a Google-style docstring.
- **Security**: Never hardcode credentials. Use `os.environ` or `.env` files.
- **Testing**: Always generate test cases for new logic. Always check errors explicitly (e.g., `require.NoError`).
- **Error Handling**: Never ignore errors in tests or production code. Handle them or fail the test.
- **Commit Messages**: Follow conventional commits (e.g., `feat:`, `fix:`).
- **Proactive Inquiry**: If a task is ambiguous, ask for clarification before starting [7].
