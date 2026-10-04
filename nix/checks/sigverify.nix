# The security-key signature verifier: its tests with a software
# authenticator, and the shipped command over a real signed tag.
{ pkgs, packages, ... }:
let
  # OpenSSH's sk-dummy: the software FIDO authenticator from OpenSSH's own
  # regression suite. It signs with whatever flags the client asks for, so
  # a key stub without the touch-required flag gets a signature with the
  # user-presence bit clear, exactly as a real key does. That is the
  # signature this verifier exists to refuse, made by real ssh-keygen and
  # with no hardware.
  #
  # It is built from the same source as the ssh-keygen the checks run,
  # because a provider must speak its OpenSSH's middleware API version.
  # Only the checks use it.
  sk-dummy = pkgs.openssh.overrideAttrs {
    pname = "openssh-sk-dummy";
    outputs = [ "out" ];
    buildPhase = ''
      runHook preBuild
      make -j$NIX_BUILD_CORES openbsd-compat/libopenbsd-compat.a
      make -j$NIX_BUILD_CORES regress/misc/sk-dummy/sk-dummy.so
      runHook postBuild
    '';
    doCheck = false;
    doInstallCheck = false;
    installPhase = ''
      runHook preInstall
      install -Dm755 regress/misc/sk-dummy/sk-dummy.so $out/lib/sk-dummy.so
      runHook postInstall
    '';
    postInstall = "";
  };
  provider = "${sk-dummy}/lib/sk-dummy.so";
in
{
  # The Go tests, with the security-key cases switched on: real
  # ssh-keygen signing through sk-dummy, including a key stub rewritten
  # to skip the touch, which ssh-keygen -Y verify and git verify-tag both
  # accept and sigverify must refuse.
  sigverify-unit = packages.sigverify-unwrapped.overrideAttrs (old: {
    env = (old.env or { }) // {
      SIGVERIFY_SK_PROVIDER = provider;
    };
  });

  # The shipped command, wrapper and all, run the way a consumer runs it.
  sigverify-e2e = pkgs.runCommand "valley-sigverify-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.openssh
      packages.sigverify
    ];
    SSH_SK_PROVIDER = provider;
  } (builtins.readFile ./sigverify-e2e.sh);
}
