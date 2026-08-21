# Author A Sandbox Spec

Use this workflow when the user describes an environment they want to create and you need to turn that into a concrete sandbox spec.

## Steps

1. Ask what the sandbox is for.
   - Number and type of resources.
   - Which existing pools should supply them.
   - Whether the user needs a quick one-off environment or a repeatable spec file.
2. Inspect available pools from the running daemon before inventing pool names.
   - Use `boxy status` for a quick health check.
   - Use the server API or existing config if you need exact pool names and expected resource types.
3. Write the sandbox spec file.
4. Submit with `boxy sandbox create -f <file> --no-wait` when you want fast acceptance, or omit `--no-wait` when waiting is acceptable.
5. Poll with `boxy sandbox get <id>` until the sandbox is `ready` or `failed`.
6. For guests that return credentials, use `boxy sandbox create -f <file> --save-guest-cred` to place the one-time credential in the OS keyring, or capture the default one-time output immediately. Use `--guest-password-stdin` or `BOXY_GUEST_PASSWORD` with `boxy sandbox exec`; never put a password in a command-line flag.
7. Use `boxy sandbox list` to confirm overall inventory and `boxy sandbox delete <id>` to clean up test runs.

## Quality Bar

- Pool names must exist.
- Requested counts must match the user goal.
- Do not assume synchronous provisioning; sandbox creation is asynchronous.
- If a sandbox fails, switch to the diagnosis workflow rather than iterating blindly.
