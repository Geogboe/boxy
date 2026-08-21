# Compose secret-backend reference

This is a deployment shape for a Boxy server using the portable `file`
backend. The named volume is writable by the Boxy process and should be
restricted to that service in a production Compose deployment.

The example does not embed a credential. After the server is running, seed a
pool through the authenticated admin operation or the CLI stdin command:

```text
boxy pool set-guest-credential win-vm --value - < bootstrap.txt
```

For Windows Server, select `dpapi` instead of `file` and protect the backend
file with the service account's ACL.
