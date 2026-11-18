"""The owner-only broker settings file, ``broker.yaml``.

The login command writes this file and the broker operations read it. It holds
the broker session (the token the broker issued at login) and which account to
read. It never holds the password or a second-factor code: those are typed at
the login prompt, handed to the broker once and forgotten.

The file always lives at ``<data dir>/broker.yaml``. There is no option to name
another file, and the helper refuses to write through a symbolic link or over
anything that is not a regular file. It is written with mode 0600, and the
helper refuses to read it when anyone but its owner could.

The format is a small subset of YAML that the helper writes and reads itself,
so it needs no YAML library: comment lines, blank lines, and ``key: value``
lines whose value is a JSON string or a whole number. Every error message names
the file or a key, never a value, because the values are secrets.
"""

from __future__ import annotations

import json
import os
import re
import secrets
import stat
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

FILE_NAME = "broker.yaml"
FORMAT_VERSION = 1

_HEADER = """\
# Sherwood's broker session, written by `python3 -m brokerhelper login`.
# Only the broker helper reads this file. Keep it owner-only (mode 600) and
# never copy it anywhere: anyone holding it can act on the broker account.
# To replace it, run the login command again.
"""

_LINE = re.compile(r"^([a-z_]+): (.+)$")
_KEYS = (
    "version",
    "account_number",
    "token_type",
    "access_token",
    "refresh_token",
    "expires_at",
    "logged_in_at",
)


class BrokerFileError(Exception):
    """broker.yaml cannot be used. The message never carries a value from it."""


class BrokerFileMissing(BrokerFileError):
    """There is no broker.yaml yet: nobody has logged in."""


@dataclass(frozen=True)
class BrokerSession:
    """What the login command saves and the broker operations use."""

    account_number: str
    token_type: str
    access_token: str
    refresh_token: str
    expires_at: datetime
    logged_in_at: datetime

    def __repr__(self) -> str:
        # A session printed by mistake shows no secret.
        return "BrokerSession(...)"

    __str__ = __repr__


def path_in(data_dir: str | os.PathLike[str]) -> Path:
    """The one place broker.yaml lives: directly inside the data folder."""
    return Path(data_dir) / FILE_NAME


def check_target(data_dir: str | os.PathLike[str]) -> Path:
    """Return the path the login command may write, or raise BrokerFileError.

    The data folder must be an existing directory reached without a symbolic
    link at its last step, and broker.yaml, if it is already there, must be a
    regular file and not a link.
    """
    folder = Path(data_dir)
    if not folder.is_absolute():
        raise BrokerFileError(f"the data folder must be an absolute path, not {folder}")
    try:
        st = os.lstat(folder)
    except FileNotFoundError:
        raise BrokerFileError(f"the data folder {folder} does not exist") from None
    if stat.S_ISLNK(st.st_mode):
        raise BrokerFileError(f"the data folder {folder} is a symbolic link; give its real path")
    if not stat.S_ISDIR(st.st_mode):
        raise BrokerFileError(f"{folder} is not a folder")

    target = path_in(folder)
    try:
        st = os.lstat(target)
    except FileNotFoundError:
        return target
    if not stat.S_ISREG(st.st_mode):
        raise BrokerFileError(f"{target} exists and is not a regular file; move it aside first")
    return target


def _timestamp(t: datetime) -> str:
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def render(session: BrokerSession) -> str:
    """The file's text for session."""
    values: dict[str, object] = {
        "version": FORMAT_VERSION,
        "account_number": session.account_number,
        "token_type": session.token_type,
        "access_token": session.access_token,
        "refresh_token": session.refresh_token,
        "expires_at": _timestamp(session.expires_at),
        "logged_in_at": _timestamp(session.logged_in_at),
    }
    lines = [f"{key}: {json.dumps(values[key])}" for key in _KEYS]
    return _HEADER + "\n".join(lines) + "\n"


def write(data_dir: str | os.PathLike[str], session: BrokerSession) -> Path:
    """Write broker.yaml in the data folder, owner-only, replacing any old one.

    The new file is written beside the old one under a temporary name, synced,
    and renamed over it, so a crash leaves either the old file or the new one,
    never half of one.
    """
    target = check_target(data_dir)
    folder = target.parent
    temp = folder / f".{FILE_NAME}.{secrets.token_hex(8)}.tmp"
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(temp, flags, 0o600)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(render(session))
            f.flush()
            os.fsync(f.fileno())
        # Check again just before the rename, in case something appeared.
        check_target(folder)
        os.replace(temp, target)
    except BaseException:
        try:
            os.unlink(temp)
        except FileNotFoundError:
            pass
        raise
    dir_fd = os.open(folder, os.O_RDONLY)
    try:
        os.fsync(dir_fd)
    finally:
        os.close(dir_fd)
    return target


def _parse_time(key: str, value: object) -> datetime:
    if not isinstance(value, str):
        raise BrokerFileError(f"{FILE_NAME}: {key} must be a timestamp")
    try:
        t = datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        raise BrokerFileError(f"{FILE_NAME}: {key} must be a timestamp") from None
    return t.replace(tzinfo=timezone.utc)


def parse(text: str) -> BrokerSession:
    """Read the file's text, or raise BrokerFileError naming the problem."""
    values: dict[str, object] = {}
    for number, line in enumerate(text.splitlines(), start=1):
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        match = _LINE.match(line)
        if match is None:
            raise BrokerFileError(f"{FILE_NAME}: line {number} is not a 'key: value' line")
        key, raw = match.groups()
        if key not in _KEYS:
            raise BrokerFileError(f"{FILE_NAME}: line {number} has an unknown key {key}")
        if key in values:
            raise BrokerFileError(f"{FILE_NAME}: {key} appears twice")
        try:
            values[key] = json.loads(raw)
        except ValueError:
            raise BrokerFileError(f"{FILE_NAME}: the value of {key} is not readable") from None

    missing = [key for key in _KEYS if key not in values]
    if missing:
        raise BrokerFileError(f"{FILE_NAME}: {missing[0]} is missing")
    if values["version"] != FORMAT_VERSION or isinstance(values["version"], bool):
        raise BrokerFileError(f"{FILE_NAME}: version must be {FORMAT_VERSION}")
    for key in ("account_number", "token_type", "access_token", "refresh_token"):
        value = values[key]
        if not isinstance(value, str) or (key != "refresh_token" and not value):
            raise BrokerFileError(f"{FILE_NAME}: {key} must be a non-empty string")
    return BrokerSession(
        account_number=str(values["account_number"]),
        token_type=str(values["token_type"]),
        access_token=str(values["access_token"]),
        refresh_token=str(values["refresh_token"]),
        expires_at=_parse_time("expires_at", values["expires_at"]),
        logged_in_at=_parse_time("logged_in_at", values["logged_in_at"]),
    )


def read(data_dir: str | os.PathLike[str]) -> BrokerSession:
    """Read broker.yaml from the data folder.

    Raises BrokerFileMissing when there is none, and BrokerFileError when it is
    a link, not a regular file, not owned by this user, readable or writable by
    anyone else, or malformed.
    """
    target = path_in(data_dir)
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(target, flags)
    except FileNotFoundError:
        raise BrokerFileMissing(f"{target} does not exist") from None
    except OSError:
        # O_NOFOLLOW on a link, or no permission.
        raise BrokerFileError(f"{target} cannot be opened; it must be a regular file") from None
    with os.fdopen(fd, "r", encoding="utf-8") as f:
        st = os.fstat(f.fileno())
        if not stat.S_ISREG(st.st_mode):
            raise BrokerFileError(f"{target} is not a regular file")
        if st.st_uid != os.geteuid():
            raise BrokerFileError(f"{target} belongs to another user")
        if stat.S_IMODE(st.st_mode) & 0o077:
            raise BrokerFileError(
                f"{target} can be read or changed by other users; run chmod 600 on it"
            )
        try:
            text = f.read(64 * 1024)
        except UnicodeDecodeError:
            raise BrokerFileError(f"{target} is not UTF-8 text") from None
    return parse(text)
