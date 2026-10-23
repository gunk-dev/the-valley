# Test data for the identity compiler

These files are test vectors. Nothing here belongs in a registry.

## The-valley operator's approval key

`valley-approve.*`, `yubico-f9.pem` and `spike.*` are the-valley operator's own approval key: a
YubiKey Ed25519 key in PIV slot 9c, the certificates the YubiKey wrote for it, and a text it signed.
They are public. They are here because only a real device's chain verifies to Yubico Attestation
Root 1, and the compiler's tests need one that does.

Never enroll this key in any other registry. Enrolling it would grant the-valley operator's YubiKey
approval rights there.

- `valley-approve.pub` and `valley-approve.pub.pem`: the public key, in SSH and PKIX form.
- `valley-approve.attest.pem`: the slot certificate.
- `yubico-f9.pem`: the device's f9 certificate.
- `valley-approve.report`: what the compiler reports for the attestation.
- `spike.txt` and `spike.sig.b64`: a 2075-byte text and the key's signature over it, made with a
  touch.
- `compiled/`: the approval signers files the identity-e2e check compiles from this key.

## The example chain

`example-root.pem`, `example-f9.pem` and `example-slot.pem` are the attestation in
[examples/identity/attested/](../../examples/identity/attested/registry.cue). They chain to a
test-only root, "the-valley example attestation root, not Yubico", and to nothing Yubico signed. The
compiler refuses them under its pinned Yubico roots, and its tests check that they verify under this
test-only root alone.
