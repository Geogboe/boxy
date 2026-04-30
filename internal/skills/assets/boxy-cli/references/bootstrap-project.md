# Bootstrap A Boxy Project

Use this workflow when the user wants a first working `boxy.yaml`, a smoke-tested daemon, and proof that the host can actually satisfy the requested pool type.

## Steps

1. Inspect host capabilities before proposing config.
   - Containers: check Docker or other supported local tooling.
   - Windows VM scenarios: check Hyper-V availability when relevant.
2. Run `boxy init` in the target directory if a config does not exist yet.
3. Edit `boxy.yaml` to define the smallest useful pool for the host.
4. Run `boxy config validate`.
5. Smoke-test with `boxy serve --once` if the task is validation only.
6. Start `boxy serve` if the user needs the daemon running.
7. Confirm readiness with `boxy status`.
8. If sandbox behavior matters, create a throwaway sandbox with `boxy sandbox create`, inspect it with `boxy sandbox get`, and remove it with `boxy sandbox delete`.

## Decision Points

- If the host cannot satisfy the requested provider type, stop and redesign the pool instead of forcing a broken config.
- If validation passes but `serve --once` fails, diagnose provider/environment issues before adding more config complexity.
- If the user wants a multi-resource environment, get one pool working first, then expand.