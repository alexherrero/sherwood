"""The one-time broker login: ``python3 -m brokerhelper login``.

Only the operator runs this, in a terminal on the NAS, inside the container::

    docker exec -it sherwood python3 -m brokerhelper login

It asks for the broker username, password and, if the account uses an
authenticator app, the current code. The library then follows the broker's
second-factor steps (an approval in the broker's app, or a code by text or
email, which it asks for on the terminal). When the broker issues a session,
the command asks which account to use if there is more than one, and writes
``<data dir>/broker.yaml``, owner-only.

The password and the code are handed to the broker once and are never written
anywhere. Nothing this module prints includes the password, a code or a token.
"""

from __future__ import annotations

import argparse
import getpass
import sys
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import IO, Any, Callable

from . import brokerfile
from .library import BrokerAuthError, BrokerCallError, Library, RobinStocks, Session
from .ops import DEFAULT_DATA_DIR


@dataclass
class Terminal:
    """Where the login command asks its questions and says what happened."""

    ask: Callable[[str], str]
    ask_secret: Callable[[str], str]
    out: IO[str]
    err: IO[str]

    def say(self, message: str) -> None:
        print(message, file=self.out, flush=True)

    def fail(self, message: str) -> None:
        print(f"brokerhelper login: {message}", file=self.err, flush=True)


def real_terminal() -> Terminal:
    return Terminal(ask=input, ask_secret=getpass.getpass, out=sys.stdout, err=sys.stderr)


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def _masked(number: Any) -> str:
    text = number if isinstance(number, str) else ""
    return f"ending {text[-4:]}" if len(text) >= 4 else "with a short number"


def _describe(record: dict[str, Any]) -> str:
    kinds = [
        record.get(key)
        for key in ("brokerage_account_type", "type")
        if isinstance(record.get(key), str) and record.get(key)
    ]
    kind = ", ".join(kinds) if kinds else "type not given"
    return f"account {_masked(record.get('account_number'))} ({kind})"


def _choose_account(term: Terminal, records: list[dict[str, Any]]) -> str | None:
    usable = [
        r
        for r in records
        if isinstance(r, dict)
        and isinstance(r.get("account_number"), str)
        and r.get("account_number")
        and not r.get("deactivated")
    ]
    if not usable:
        term.fail("the broker listed no open account. Nothing was written.")
        return None
    if len(usable) == 1:
        term.say(f"Using {_describe(usable[0])}.")
        return str(usable[0]["account_number"])
    term.say("The broker lists more than one account:")
    for i, record in enumerate(usable, start=1):
        term.say(f"  {i}. {_describe(record)}")
    answer = term.ask(f"Which one should sherwood read (1-{len(usable)})? ").strip()
    if not answer.isdigit() or not 1 <= int(answer) <= len(usable):
        term.fail("that is not one of the listed numbers. Nothing was written.")
        return None
    return str(usable[int(answer) - 1]["account_number"])


def run(
    data_dir: str,
    library: Library,
    term: Terminal,
    now: Callable[[], datetime] = _utcnow,
) -> int:
    """Run the login. Returns the process exit code."""
    try:
        target = brokerfile.check_target(data_dir)
    except brokerfile.BrokerFileError as exc:
        term.fail(f"{exc}. Nothing was written.")
        return 2

    term.say(f"This logs sherwood in to the broker and saves the session in {target}.")
    username = term.ask("Broker username: ").strip()
    if not username:
        term.fail("no username given. Nothing was written.")
        return 1
    password = term.ask_secret("Broker password (not shown): ")
    if not password:
        term.fail("no password given. Nothing was written.")
        return 1
    code = term.ask(
        "Code from your authenticator app, or press Enter if you don't use one: "
    ).strip()

    term.say(
        "Logging in. If the broker asks you to approve this login in its app, or sends "
        "you a code by text or email, follow its prompts below."
    )
    try:
        reply = library.login(username, password, code or None)
    except BrokerAuthError:
        term.fail("the broker did not accept the login, or it did not finish. Nothing was written.")
        return 1
    except BrokerCallError as exc:
        term.fail(f"{exc.safe_message()}. Nothing was written.")
        return 1
    except Exception as exc:  # noqa: BLE001 - only the type is shown
        term.fail(f"the login failed with {type(exc).__name__}. Nothing was written.")
        return 1
    finally:
        del password

    token_type = reply.get("token_type")
    access_token = reply.get("access_token")
    refresh_token = reply.get("refresh_token") or ""
    expires_in = reply.get("expires_in")
    if (
        not isinstance(token_type, str)
        or not token_type
        or not isinstance(access_token, str)
        or not access_token
        or not isinstance(refresh_token, str)
        or isinstance(expires_in, bool)
        or not isinstance(expires_in, int)
        or expires_in <= 0
    ):
        term.fail("the broker's login answer was not in the expected shape. Nothing was written.")
        return 1

    logged_in_at = now()
    session = Session(token_type, access_token)
    try:
        records = library.accounts(session)
    except BrokerAuthError:
        term.fail("the broker refused the new session. Nothing was written.")
        return 1
    except BrokerCallError as exc:
        term.fail(f"reading the account list: {exc.safe_message()}. Nothing was written.")
        return 1
    except Exception as exc:  # noqa: BLE001
        term.fail(f"reading the account list failed with {type(exc).__name__}. Nothing was written.")
        return 1
    account_number = _choose_account(term, records)
    if account_number is None:
        return 1

    saved = brokerfile.BrokerSession(
        account_number=account_number,
        token_type=token_type,
        access_token=access_token,
        refresh_token=refresh_token,
        expires_at=logged_in_at + timedelta(seconds=expires_in),
        logged_in_at=logged_in_at,
    )
    try:
        path = brokerfile.write(data_dir, saved)
    except brokerfile.BrokerFileError as exc:
        term.fail(f"{exc}. Nothing was written.")
        return 1
    except OSError as exc:
        term.fail(f"could not write {target}: {exc.strerror or type(exc).__name__}.")
        return 1
    expires = saved.expires_at.strftime("%Y-%m-%d %H:%M UTC")
    term.say(f"Logged in. The session is saved in {path}, readable only by you.")
    term.say(f"The broker says it lasts until about {expires}; log in again after that.")
    return 0


def main(argv: list[str], term: Terminal | None = None, library: Library | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="brokerhelper login",
        description="Log sherwood in to the broker and save the session in broker.yaml.",
    )
    parser.add_argument(
        "--data-dir",
        default=DEFAULT_DATA_DIR,
        help="the application-data folder that holds broker.yaml (default %(default)s)",
    )
    args = parser.parse_args(argv)
    if term is None:
        if not sys.stdin.isatty():
            print(
                "brokerhelper login: run this in a terminal, for example "
                "`docker exec -it sherwood python3 -m brokerhelper login`.",
                file=sys.stderr,
            )
            return 2
        term = real_terminal()
    return run(args.data_dir, library if library is not None else RobinStocks(), term)
