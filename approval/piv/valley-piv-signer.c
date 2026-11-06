/*
 * valley-piv-signer: one PIV session on a YubiKey for a run of approval
 * signatures. The approval command (approval/piv.go) starts it, sends it
 * statements one at a time, and reads back raw Ed25519 signatures. The
 * helper holds the YubiKey for the whole run, so the PIN is asked for once
 * and the key's touch policy asks for a touch on every signature.
 *
 *   valley-piv-signer [--slot HEX] [--reader NAME]
 *
 * The helper first writes one line, the key it signs with:
 *
 *   key <serial> <hex of the slot's attestation certificate, DER>
 *
 * Then, for each request on stdin,
 *
 *   sign <length> <session|each|none>\n<length bytes of statement text>
 *
 * it writes "touch" just before the device signs, then the signature:
 *
 *   signature <hex of the 64-byte Ed25519 signature>
 *
 * A refusal is one line in place of either reply, and the helper reads no
 * request after it:
 *
 *   error <device|pin|pin-format|cancelled|touch|request> <detail>
 *
 * The YubiKey is the helper's alone for the session: it connects in
 * exclusive mode, so no other program can send the card a command while
 * the PIN is verified. libykpiv answers a reset or a removed card by
 * reconnecting (_ykpiv_begin_transaction), in shared mode upstream and in
 * exclusive mode in the copy packaged with the helper (nix/packages.nix).
 * Before each call into libykpiv the helper asks pcscd whether the card
 * was reset or removed, and ends the session if it was, so no session
 * carries on with a card that changed. A new session asks for the PIN
 * again.
 *
 * Every exit resets the YubiKey, which forgets the verified PIN. pcscd
 * leaves the card as it is when a client dies, so a signal does not kill
 * the helper: every signal that would end it (SIGINT, SIGTERM, SIGHUP,
 * SIGQUIT, and the ones nothing here uses, such as SIGUSR1 or SIGALRM)
 * ends the session, which disconnects with a reset, and the helper then
 * exits by the same signal. Those signals are held while the helper
 * decides whether to wait for input and let through only inside the wait,
 * so none is lost between the decision and the wait. SIGPIPE is ignored,
 * and a reply that cannot be written ends the session. Fault signals
 * (SIGSEGV, SIGBUS, SIGFPE, SIGILL, SIGABRT) keep their default: a
 * process that faulted cannot reset the card reliably. SIGKILL cannot be
 * caught. Both leave the PIN verified until the YubiKey is reset or
 * removed.
 *
 * The PIN is read from the controlling terminal with echo off, never from
 * the arguments, the environment or stdin, and is never written anywhere
 * but to the device. The process cannot be traced or dump core. It is
 * asked for whenever the device says the PIN is not verified, which in one
 * session is at the first signature. "each" asks for it before every
 * signature, for a key whose PIN policy is always, and "none" never asks,
 * for a key whose PIN policy is never. A PIN the device refuses is
 * reported, with the tries left, and never sent again. The PIN is verified
 * with a raw VERIFY so that libykpiv keeps no copy of it.
 */
#define _GNU_SOURCE

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/prctl.h>
#include <termios.h>
#include <unistd.h>

#include <winscard.h>
#include <ykpiv/ykpiv.h>

/* The largest statement the helper signs: approval.MaxStatement. */
#define MAX_STATEMENT 1024
#define PIN_MIN 6
#define PIN_MAX 8

static struct termios saved;
/* The signal that ends the session, 0 while none has. */
static volatile sig_atomic_t stopped;
/* The signals that end the session. */
static sigset_t ending;
/* Set when a reply could not be written: the reader is gone. */
static int broken;
/* The errno of a failed wait or read on stdin or the terminal, 0 while
 * none has failed. An I/O error ends the session like a refusal, apart
 * from the end of input, which ends it clean. */
static int failed;

static SCARDCONTEXT context;
static SCARDHANDLE card;
static int established, connected;
/* Set once libykpiv holds the card and context. From then on they are
 * closed through it: when SCardBeginTransaction fails inside a call,
 * _ykpiv_begin_transaction may reset and disconnect the card, release
 * the context and open replacements, which only libykpiv's state names. */
static int handed;

static void restore_tty(int fd) {
  while (tcsetattr(fd, TCSAFLUSH, &saved) != 0 && errno == EINTR) {
  }
}

/* A signal ends the session in the main loop, which restores the terminal
 * and resets the YubiKey. */
static void on_signal(int sig) { stopped = sig; }

/* Exits by the signal that ended the session, once the YubiKey is reset.
 * The ending signals are blocked while stopped is read, so a second one
 * cannot change it between restoring the default and raising; the raised
 * signal is delivered, to its default, when they are unblocked. Returns
 * when no signal ended the session. */
static void exit_by_signal(void) {
  sigprocmask(SIG_BLOCK, &ending, NULL);
  int sig = stopped;
  if (sig) {
    signal(sig, SIG_DFL);
    raise(sig);
  }
  sigprocmask(SIG_UNBLOCK, &ending, NULL);
}

/* Waits until fd has input or a signal ends the session. The ending
 * signals are blocked while stopped is checked and let through only inside
 * ppoll, so one arriving between the check and the wait ends the wait.
 * Returns 0 for input, -1 when the session ended or the wait failed. */
static int wait_readable(int fd) {
  sigset_t was;
  sigprocmask(SIG_BLOCK, &ending, &was);
  struct pollfd pfd = {fd, POLLIN, 0};
  int r = -1;
  while (!stopped) {
    r = ppoll(&pfd, 1, NULL, &was);
    if (r > 0) {
      break;
    }
    if (r < 0 && errno != EINTR) {
      failed = errno;
      break;
    }
  }
  sigprocmask(SIG_SETMASK, &was, NULL);
  return stopped || r <= 0 ? -1 : 0;
}

/* Reads n bytes from fd, fewer at the end of input, when a signal ends
 * the session, or when a read fails. */
static size_t read_in(int fd, void *buf, size_t n) {
  size_t got = 0;
  while (got < n && wait_readable(fd) == 0) {
    ssize_t r = read(fd, (char *)buf + got, n - got);
    if (r < 0 && errno == EINTR) {
      continue;
    }
    if (r < 0) {
      failed = errno;
    }
    if (r <= 0) {
      break;
    }
    got += (size_t)r;
  }
  return got;
}

/* Reads a line of at most cap - 1 bytes into line, NUL-terminated, and
 * returns its length with the newline, 0 at the end of input or when
 * reading failed. A longer line is cut, and fails the request's parse. */
static size_t read_line(int fd, char *line, size_t cap) {
  size_t n = 0;
  while (n < cap - 1 && read_in(fd, line + n, 1) == 1) {
    if (line[n++] == '\n') {
      break;
    }
  }
  line[n] = 0;
  return n;
}

static void reply(const char *fmt, ...) {
  va_list ap;
  va_start(ap, fmt);
  vfprintf(stdout, fmt, ap);
  va_end(ap);
  fputc('\n', stdout);
  if (fflush(stdout) != 0) {
    broken = 1;
  }
}

static void reply_hex(const char *word, const char *prefix, const unsigned char *b, size_t n) {
  fputs(word, stdout);
  fputc(' ', stdout);
  fputs(prefix, stdout);
  for (size_t i = 0; i < n; i++) {
    fprintf(stdout, "%02x", b[i]);
  }
  fputc('\n', stdout);
  if (fflush(stdout) != 0) {
    broken = 1;
  }
}

/* Connects to the first reader whose name holds wanted (any, when NULL)
 * that takes an exclusive connection. Replies with the refusal when none
 * does. */
static int connect_exclusive(const char *wanted) {
  LONG rc = SCardEstablishContext(SCARD_SCOPE_SYSTEM, NULL, NULL, &context);
  if (rc != SCARD_S_SUCCESS) {
    reply("error device no YubiKey could be reached: %s", pcsc_stringify_error(rc));
    return -1;
  }
  established = 1;
  char readers[2048];
  DWORD len = sizeof readers;
  rc = SCardListReaders(context, NULL, readers, &len);
  int busy = 0;
  for (const char *r = readers; rc == SCARD_S_SUCCESS && *r; r += strlen(r) + 1) {
    int holds = wanted == NULL;
    for (const char *p = r; !holds && strlen(p) >= strlen(wanted); p++) {
      holds = strncasecmp(p, wanted, strlen(wanted)) == 0;
    }
    DWORD protocol = 0;
    if (holds) {
      LONG c = SCardConnect(context, r, SCARD_SHARE_EXCLUSIVE, SCARD_PROTOCOL_T0 | SCARD_PROTOCOL_T1,
                            &card, &protocol);
      if (c == SCARD_S_SUCCESS) {
        connected = 1;
        return 0;
      }
      busy |= c == SCARD_E_SHARING_VIOLATION;
    }
  }
  if (busy) {
    reply("error device another program is using it");
  } else {
    reply("error device no YubiKey could be reached: %s",
          pcsc_stringify_error(rc == SCARD_S_SUCCESS ? SCARD_E_NO_SMARTCARD : rc));
  }
  return -1;
}

/* Says whether the card is the one connected to, replying with the
 * refusal when it is not. pcscd flags a reset or a removal on the
 * connection until it is reconnected, so the flag is seen here before
 * libykpiv's next call would reconnect. */
static int exclusive(void) {
  LONG rc = SCardStatus(card, NULL, NULL, NULL, NULL, NULL, NULL);
  if (rc == SCARD_S_SUCCESS) {
    return 1;
  }
  if (rc == SCARD_W_RESET_CARD || rc == SCARD_W_REMOVED_CARD) {
    reply("error device it was reset or removed during the session");
  } else {
    reply("error device %s", pcsc_stringify_error(rc));
  }
  return 0;
}

/* Reads the PIN from the terminal. Returns its length, 0 when none was
 * entered (an empty line, end of input, or a signal), -1 when there is no
 * terminal, and -2 when what was entered is not 6 to 8 characters. */
static int read_pin(char pin[PIN_MAX + 1], uint32_t serial, int slot) {
  int fd = open("/dev/tty", O_RDWR | O_NOCTTY | O_CLOEXEC);
  if (fd < 0) {
    return -1;
  }
  if (tcgetattr(fd, &saved) != 0) {
    close(fd);
    return -1;
  }
  /* Stopping the helper while echo is off would leave the shell without
   * it, so job control waits until the PIN is read. */
  const int held[] = {SIGTSTP, SIGTTIN, SIGTTOU};
  struct sigaction ignore, before[3];
  memset(&ignore, 0, sizeof ignore);
  ignore.sa_handler = SIG_IGN;
  for (int i = 0; i < 3; i++) {
    sigaction(held[i], &ignore, &before[i]);
  }
  struct termios quiet = saved;
  quiet.c_lflag &= ~(tcflag_t)ECHO;
  char prompt[80];
  int n = snprintf(prompt, sizeof prompt, "PIN for YubiKey %u, slot %02x: ", serial, slot);
  size_t len = 0;
  int over = 0, ended = 0;
  if (tcsetattr(fd, TCSAFLUSH, &quiet) == 0 && write(fd, prompt, (size_t)n) == n) {
    char c = 0;
    while (wait_readable(fd) == 0) {
      ssize_t r = read(fd, &c, 1);
      if (r < 0 && errno == EINTR) {
        continue;
      }
      if (r < 0) {
        failed = errno;
      }
      if (r <= 0) {
        break;
      }
      if (c == '\n' || c == '\r') {
        ended = 1;
        break;
      }
      if (len < PIN_MAX) {
        pin[len++] = c;
      } else {
        over = 1;
      }
    }
    explicit_bzero(&c, sizeof c);
    if (write(fd, "\n", 1) < 0) {
      /* The prompt's line stays open; nothing else depends on it. */
    }
  } else {
    n = -1;
  }
  restore_tty(fd);
  for (int i = 0; i < 3; i++) {
    sigaction(held[i], &before[i], NULL);
  }
  close(fd);
  pin[len] = 0;
  if (n < 0) {
    return -1;
  }
  if (!ended || stopped || len == 0) {
    explicit_bzero(pin, PIN_MAX + 1);
    return 0;
  }
  if (over || len < PIN_MIN) {
    explicit_bzero(pin, PIN_MAX + 1);
    return -2;
  }
  return (int)len;
}

/* Sends VERIFY with the PIN. 0 is verified, a positive number the tries
 * left after a refusal, -1 a blocked PIN, and -2 a device failure.
 * libykpiv copies the APDU onto its own stack and does not wipe it; the
 * process neither dumps core nor can be traced, so the copy stays in it. */
static int verify_pin(ykpiv_state *state, const char *pin, size_t len) {
  const unsigned char templ[4] = {0x00, 0x20, 0x00, 0x80};
  unsigned char data[PIN_MAX];
  memset(data, 0xff, sizeof data);
  memcpy(data, pin, len);
  unsigned char out[32];
  unsigned long out_len = sizeof out;
  int sw = 0;
  ykpiv_rc rc = ykpiv_transfer_data(state, templ, data, sizeof data, out, &out_len, &sw);
  explicit_bzero(data, sizeof data);
  if (rc != YKPIV_OK) {
    return -2;
  }
  if (sw == 0x9000) {
    return 0;
  }
  if ((sw & 0xfff0) == 0x63c0) {
    return (sw & 0xf) == 0 ? -1 : (sw & 0xf);
  }
  if (sw == 0x6983) {
    return -1;
  }
  return -2;
}

/* Makes sure the PIN is verified before a signature, asking for it when
 * the device says it is not, or always when each is set. Returns 0 when
 * the PIN is verified; otherwise it has replied with the refusal, unless
 * a signal ended the session. */
static int ensure_pin(ykpiv_state *state, uint32_t serial, int slot, int each) {
  if (!exclusive()) {
    return -1;
  }
  int tries = 0;
  ykpiv_rc rc = ykpiv_verify(state, NULL, &tries);
  if (rc == YKPIV_OK && !each) {
    return 0;
  }
  if (rc == YKPIV_PIN_LOCKED) {
    reply("error pin 0");
    return -1;
  }
  if (rc != YKPIV_OK && rc != YKPIV_WRONG_PIN) {
    reply("error device %s", ykpiv_strerror(rc));
    return -1;
  }
  char pin[PIN_MAX + 1];
  int len = read_pin(pin, serial, slot);
  int ret = -1;
  if (stopped) {
    /* A signal ends the session without a reply. */
  } else if (failed) {
    reply("error device the terminal could not be read: %s", strerror(failed));
  } else if (len == -1) {
    reply("error device there is no terminal to ask for the PIN on");
  } else if (len == -2) {
    reply("error pin-format");
  } else if (len == 0) {
    reply("error cancelled");
  } else {
    int got = exclusive() ? verify_pin(state, pin, (size_t)len) : -3;
    if (got == 0) {
      ret = 0;
    } else if (got == -2) {
      reply("error device the PIN could not be sent to the YubiKey");
    } else if (got != -3) {
      reply("error pin %d", got < 0 ? 0 : got);
    }
  }
  /* The one exit: the PIN leaves the stack whichever way the check went. */
  explicit_bzero(pin, sizeof pin);
  return ret;
}

static int parse_slot(const char *s) {
  char *end = NULL;
  long v = strtol(s, &end, 16);
  if (!*s || *end || v < 0) {
    return -1;
  }
  if (v == YKPIV_KEY_AUTHENTICATION || v == YKPIV_KEY_SIGNATURE || v == YKPIV_KEY_KEYMGM ||
      v == YKPIV_KEY_CARDAUTH || (v >= YKPIV_KEY_RETIRED1 && v <= YKPIV_KEY_RETIRED20)) {
    return (int)v;
  }
  return -1;
}

/* Reads one request. Returns the statement's length, 0 at the end of
 * input or after a signal, and -1 after replying with the refusal, which
 * a failed read is too. */
static long read_request(unsigned char *buf, int *each, int *none) {
  char line[64];
  if (read_line(STDIN_FILENO, line, sizeof line) == 0 || stopped) {
    if (failed && !stopped) {
      reply("error request the request could not be read: %s", strerror(failed));
      return -1;
    }
    return 0;
  }
  /* The length is one to four decimal digits: MAX_STATEMENT has four. */
  const char *p = line;
  long n = 0;
  int digits = 0;
  if (strncmp(p, "sign ", 5) == 0) {
    for (p += 5; *p >= '0' && *p <= '9' && digits <= 4; p++, digits++) {
      n = n * 10 + (*p - '0');
    }
  }
  if (digits < 1 || digits > 4 || *p++ != ' ') {
    reply("error request a request is: sign <length> <session|each|none>");
    return -1;
  }
  *each = strcmp(p, "each\n") == 0;
  *none = strcmp(p, "none\n") == 0;
  if (!*each && !*none && strcmp(p, "session\n") != 0) {
    reply("error request the PIN mode is session, each or none");
    return -1;
  }
  if (n < 1 || n > MAX_STATEMENT) {
    reply("error request a statement is 1 to %d bytes", MAX_STATEMENT);
    return -1;
  }
  if (read_in(STDIN_FILENO, buf, (size_t)n) != (size_t)n) {
    if (stopped) {
      return 0;
    }
    if (failed) {
      reply("error request the statement could not be read: %s", strerror(failed));
    } else {
      reply("error request the statement ended early");
    }
    return -1;
  }
  return n;
}

/* Signs one statement. Returns 0 after replying with the signature, and
 * -1 after replying with the refusal. */
static int sign(ykpiv_state *state, const unsigned char *statement, size_t n, int slot) {
  if (!exclusive()) {
    return -1;
  }
  reply("touch");
  if (broken) {
    return -1;
  }
  unsigned char sig[128];
  size_t sig_len = sizeof sig;
  ykpiv_rc rc = ykpiv_sign_data(state, statement, n, sig, &sig_len, YKPIV_ALGO_ED25519,
                                (unsigned char)slot);
  if (rc == YKPIV_AUTHENTICATION_ERROR) {
    /* The YubiKey refuses with 6982 both a signature not touched in time
     * and one without a verified PIN, and libykpiv returns both as
     * YKPIV_AUTHENTICATION_ERROR, so the PIN's state names the cause. */
    int tries = 0;
    if (!exclusive()) {
      return -1;
    }
    if (ykpiv_verify(state, NULL, &tries) == YKPIV_OK) {
      reply("error touch");
    } else {
      reply("error device it refused to sign without a verified PIN");
    }
    return -1;
  }
  if (rc != YKPIV_OK || sig_len != 64) {
    reply("error device the YubiKey did not sign: %s", ykpiv_strerror(rc));
    return -1;
  }
  reply_hex("signature", "", sig, sig_len);
  return 0;
}

/* Opens the session: the YubiKey in exclusive mode, its PIV application
 * selected, and the key line written. Returns 0, or -1 after replying with
 * the refusal. */
static int open_session(ykpiv_state *state, const char *reader, int slot, uint32_t *serial) {
  if (connect_exclusive(reader) != 0) {
    return -1;
  }
  ykpiv_rc rc = ykpiv_connect_with_external_card(state, (uintptr_t)context, (uintptr_t)card);
  handed = rc == YKPIV_OK;
  /* An external card is not selected for; reading the PIN's tries selects
   * the PIV application, as ykpiv_connect does, and reads the YubiKey's
   * version and serial. */
  int tries = 0;
  if (rc == YKPIV_OK && exclusive()) {
    rc = ykpiv_get_pin_retries(state, &tries);
  } else if (rc == YKPIV_OK) {
    return -1;
  }
  if (rc != YKPIV_OK) {
    reply("error device no YubiKey could be reached: %s", ykpiv_strerror(rc));
    return -1;
  }
  if (!exclusive()) {
    return -1;
  }
  rc = ykpiv_get_serial(state, serial);
  if (rc != YKPIV_OK) {
    reply("error device the YubiKey's serial number could not be read: %s", ykpiv_strerror(rc));
    return -1;
  }
  unsigned char cert[YKPIV_OBJ_MAX_SIZE];
  size_t cert_len = sizeof cert;
  if (!exclusive()) {
    return -1;
  }
  rc = ykpiv_attest(state, (unsigned char)slot, cert, &cert_len);
  if (rc != YKPIV_OK) {
    reply("error device slot %02x holds no key generated on the YubiKey: %s", slot, ykpiv_strerror(rc));
    return -1;
  }
  char prefix[16];
  snprintf(prefix, sizeof prefix, "%u ", *serial);
  reply_hex("key", prefix, cert, cert_len);
  return broken ? -1 : 0;
}

int main(int argc, char **argv) {
  prctl(PR_SET_DUMPABLE, 0);
  /* A closed standard descriptor would be reused by the next open, so the
   * pcscd socket could take stdout's place and receive the replies, or the
   * terminal stdin's and have requests read from it. */
  for (int fd = 0; fd <= 2; fd++) {
    if (fcntl(fd, F_GETFD) < 0 && errno == EBADF) {
      open("/dev/null", fd == 0 ? O_RDONLY : O_WRONLY);
    }
  }

  int slot = YKPIV_KEY_SIGNATURE;
  const char *reader = NULL;
  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "--slot") == 0 && i + 1 < argc) {
      slot = parse_slot(argv[++i]);
      if (slot < 0) {
        reply("error request the slot is one of 9a, 9c, 9d, 9e and 82 to 95");
        return 2;
      }
    } else if (strcmp(argv[i], "--reader") == 0 && i + 1 < argc) {
      reader = argv[++i];
    } else {
      reply("error request usage: valley-piv-signer [--slot HEX] [--reader NAME]");
      return 2;
    }
  }

  /* No SA_RESTART: a signal makes a blocking read return, so the session
   * ends at once. pcsc-lite's client retries its reads and writes on
   * EINTR, so a signal during a libykpiv call leaves the call whole. */
  struct sigaction sa;
  memset(&sa, 0, sizeof sa);
  sa.sa_handler = on_signal;
  sigemptyset(&sa.sa_mask);
  const int ends[] = {SIGINT,  SIGTERM,   SIGHUP,  SIGQUIT, SIGUSR1, SIGUSR2,
                      SIGALRM, SIGVTALRM, SIGPROF, SIGXCPU, SIGXFSZ, SIGIO,  SIGPWR};
  sigemptyset(&ending);
  for (size_t i = 0; i < sizeof ends / sizeof ends[0]; i++) {
    sigaction(ends[i], &sa, NULL);
    sigaddset(&ending, ends[i]);
  }
  signal(SIGPIPE, SIG_IGN);

  ykpiv_state *state = NULL;
  ykpiv_rc rc = ykpiv_init(&state, 0);
  if (rc != YKPIV_OK) {
    reply("error device %s", ykpiv_strerror(rc));
    exit_by_signal();
    return 1;
  }
  uint32_t serial = 0;
  int status = open_session(state, reader, slot, &serial) == 0 ? 0 : 1;
  static unsigned char statement[MAX_STATEMENT];
  while (status == 0 && !stopped && !broken) {
    int each = 0, none = 0;
    long n = read_request(statement, &each, &none);
    if (n == 0) {
      break;
    }
    if (n < 0 || (!none && ensure_pin(state, serial, slot, each) != 0) ||
        sign(state, statement, (size_t)n, slot) != 0) {
      status = 1;
    }
  }

  /* The reset is what makes the YubiKey forget the verified PIN. Once
   * handed, ykpiv_done disconnects with a reset whichever card libykpiv
   * holds, the helper's or a replacement, and releases its context;
   * the helper's card, if replaced, was reset when libykpiv let go of
   * it. Before that, the handles are the helper's alone. */
  if (handed) {
    ykpiv_done(state);
  } else {
    ykpiv_done_with_external_card(state);
    if (connected) {
      SCardDisconnect(card, SCARD_RESET_CARD);
    }
    if (established) {
      SCardReleaseContext(context);
    }
  }
  exit_by_signal();
  return status != 0 || broken || stopped || failed != 0;
}
