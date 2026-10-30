// The reader pages' script. It takes the read link out of the address
// bar first, and keeps it in this closure alone: never in the page, in
// storage or in a log. Nothing is sent until the reader clicks.
(function () {
  "use strict";
  let link = location.hash.slice(1);
  history.replaceState(null, "", location.pathname + location.search);

  const box = document.getElementById("reader");
  if (!box) return;
  const el = function (id) { return document.getElementById(id); };
  const notices = ["r-missing", "r-refused", "r-other", "r-unknown"];
  function show(id) {
    notices.forEach(function (n) { if (el(n)) el(n).hidden = n !== id; });
  }
  const sent = { credentials: "same-origin", cache: "no-store", redirect: "error" };
  function post(path, body) {
    return fetch(path, Object.assign({
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Valley-Reader": "1" },
      body: JSON.stringify(body),
    }, sent)).then(function (r) { return r.status; });
  }
  // signedIn asks the server which link this browser's session was
  // redeemed from: its credential ID, or "" for no session.
  function signedIn() {
    return fetch("/-/session", sent).then(function (r) {
      if (r.status !== 200) throw new Error("the session state did not arrive");
      return r.json();
    }).then(function (s) { return s.link; });
  }
  function done() {
    link = "";
    location.replace("/");
  }

  if (box.dataset.page === "logout") {
    const out = el("r-logout");
    if (!out) return;
    out.hidden = false;
    out.addEventListener("click", function () {
      out.disabled = true;
      post("/-/logout", {}).then(function (status) {
        if (status === 204) return done();
        show("r-unknown");
        out.disabled = false;
      }, function () {
        show("r-unknown");
        out.disabled = false;
      });
    });
    return;
  }

  const redeem = el("r-redeem");
  const swap = el("r-switch");
  if (!/^lk-[0-9a-f]{16}\.[A-Za-z0-9_-]{43}$/.test(link)) {
    link = "";
    show("r-missing");
    return;
  }
  const id = link.split(".")[0];
  // unsure is set once a request's outcome is unknown: no answer came, or
  // one that is neither success nor a refusal. The link may be used, and
  // this browser may hold the session it started.
  let unsure = false;
  // mode is which button is shown: redeem alone, or sign out, then redeem.
  function mode(switching) {
    redeem.hidden = switching;
    swap.hidden = !switching;
    el("r-switching").hidden = !switching;
  }
  function unknown(button) {
    unsure = true;
    show("r-unknown");
    button.disabled = false;
  }
  function answered(status, button) {
    if (status === 204) return done();
    if (status === 403) {
      link = "";
      show("r-refused");
      redeem.hidden = swap.hidden = true;
      return;
    }
    if (status !== 409) return unknown(button);
    show("r-other");
    mode(true);
    button.disabled = false;
  }
  // attempt sends the link, after a logout when signOut is set. After an
  // unknown outcome it first asks which session this browser holds. One
  // from this link means the earlier attempt succeeded, so nothing is
  // sent. With none, there is no one to sign out, and the logout is left
  // out, so a retry never ends a session the earlier attempt started.
  function attempt(button, signOut) {
    button.disabled = true;
    (unsure ? signedIn() : Promise.resolve(null)).then(function (current) {
      if (current === id) return done();
      if (current === "") signOut = false;
      return (signOut ? post("/-/logout", {}) : Promise.resolve(204)).then(function (status) {
        if (status !== 204) return unknown(button);
        return post("/-/redeem", { credential: link }).then(function (status) {
          answered(status, button);
        });
      });
    }).catch(function () { unknown(button); });
  }
  redeem.addEventListener("click", function () { attempt(redeem, false); });
  swap.addEventListener("click", function () { attempt(swap, true); });
  mode(box.dataset.session === "1");
})();
