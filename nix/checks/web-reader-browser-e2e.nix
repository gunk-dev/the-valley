# The reader page in a real browser, in the Nix build sandbox and no VM:
# the packaged valley-web behind a TLS proxy, and headless Chromium,
# driven over its automation pipe (TestBrowser, in
# web/cmd/valley-web/browser_test.go). Chromium runs with its own sandbox
# and three flags: --headless=new, a temporary --user-data-dir, and
# --remote-debugging-pipe. It trusts the proxy's certificate through a
# test CA that certutil adds to the profile's certificate database; no
# flag turns off a certificate check or any of the browser's security.
# fontconfig gives it the DejaVu fonts to shape the pages' text with.
# One browser runs, in about 2 GiB of memory; the closure holds Chromium,
# so the check is slow though it boots no machine.
#
# It holds the browser engine alone: loading the reader page takes the
# link out of the address bar and sends no redemption; the page's policy
# runs its script and lets it fetch this origin, and blocks a script put
# in the page; one click redeems the link once, the browser keeps the
# __Host- cookie, Secure, HttpOnly and SameSite=Lax, out of the page's
# reach, and the page follows /, which lands the reader, whose view holds
# app alone, on app's tree. Then the proxy spoils two answers to a
# switch to another link: a redemption answered 502 with the new
# session's cookie, and one whose answer it drops. Each time the page
# says it cannot tell whether the link was used. The first retry finds
# this link's session at /-/session and lands on app's tree with no
# second sign-out; the second finds no session, signs no one out, and is
# told the link cannot be used. HTTP contracts, isolation, archives,
# revocation, logs and permissions are the unit, binary, module and
# image checks'.
{
  pkgs,
  lib,
  packages,
  ...
}:
{
  web-reader-browser-e2e = packages.valley-web.unit.overrideAttrs (prev: {
    pname = "valley-web-reader-browser-e2e";
    subPackages = [ "cmd/valley-web" ];
    checkFlags = [ "-run=^TestBrowser$" ];
    nativeCheckInputs = prev.nativeCheckInputs ++ [
      pkgs.chromium
      pkgs.nss.tools
    ];
    # Chromium shapes the pages' text with a font fontconfig finds; with
    # none, a text-heavy page can crash its renderer.
    FONTCONFIG_FILE = pkgs.makeFontsConf { fontDirectories = [ pkgs.dejavu_fonts ]; };
    preCheck = ''
      export VALLEY_TEST_TOOLS=required
      export VALLEY_WEB=${lib.getExe packages.valley-web}
      export VALLEY_WEB_CHROMIUM=${lib.getExe pkgs.chromium}
    '';
    postCheck = "";
  });
}
