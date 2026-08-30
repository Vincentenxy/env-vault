#!/usr/bin/env python3
"""Test the internal master-key transfer endpoint without writing key material."""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import json
import os
import sys
import uuid
from http import HTTPStatus
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

DEFAULT_URL = "http://localhost:8090/internal/v1/masterKey/transfer"
TOKEN_ENV = "SECURITY_MASTER_KEY_PEER_TOKEN"
EXPECTED_KEY_ENV = "ENV_VAULT_MASTER_KEY"
ALGORITHM = "RSA-OAEP-SHA256"
TOKEN_HEADER = "X-Env-Vault-Internal-Token"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a temporary RSA key pair and test master-key transfer."
    )
    parser.add_argument("--url", default=DEFAULT_URL, help="transfer endpoint URL")
    parser.add_argument(
        "--token-env",
        default=TOKEN_ENV,
        help=f"environment variable containing the peer token (default: {TOKEN_ENV})",
    )
    parser.add_argument(
        "--expected-key-env",
        default=EXPECTED_KEY_ENV,
        help=(
            "optional environment variable containing the known Base64 master key; "
            f"default: {EXPECTED_KEY_ENV}"
        ),
    )
    parser.add_argument(
        "--instance-id", default="python-test-client", help="requesting instance identifier"
    )
    parser.add_argument("--timeout", type=float, default=10.0, help="HTTP timeout in seconds")
    return parser.parse_args()


def fail(message: str) -> int:
    print(f"FAIL: {message}", file=sys.stderr)
    return 1


def decode_json_response(raw: bytes) -> dict:
    try:
        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("server returned a non-JSON response") from exc
    if not isinstance(value, dict):
        raise ValueError("server response is not a JSON object")
    return value


def main() -> int:
    args = parse_args()
    try:
        from cryptography.hazmat.primitives import hashes, serialization
        from cryptography.hazmat.primitives.asymmetric import padding, rsa
    except ModuleNotFoundError:
        return fail(
            "missing dependency: install it with "
            "python -m pip install -r tools/requirements-master-key-test.txt"
        )

    token = os.environ.get(args.token_env, "").strip()
    if not token:
        return fail(f"{args.token_env} is not set")

    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public_key = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    ).decode("ascii")
    request_id = str(uuid.uuid4())
    payload = json.dumps(
        {
            "instanceId": args.instance_id,
            "requestId": request_id,
            "publicKey": public_key,
            "algorithm": ALGORITHM,
        }
    ).encode("utf-8")

    request = Request(
        args.url,
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            TOKEN_HEADER: token,
        },
    )

    try:
        with urlopen(request, timeout=args.timeout) as response:
            response_body = decode_json_response(response.read())
            status_code = response.status
    except HTTPError as exc:
        try:
            response_body = decode_json_response(exc.read())
            message = response_body.get("msg", "unknown server error")
        except ValueError:
            message = "server returned an invalid error response"
        return fail(f"HTTP {exc.code}: {message}")
    except URLError as exc:
        return fail(f"request failed: {exc.reason}")
    except (TimeoutError, OSError) as exc:
        return fail(f"request failed: {exc}")

    if status_code != HTTPStatus.OK:
        return fail(f"unexpected HTTP status: {status_code}")
    if response_body.get("code") != 0:
        return fail(f"server rejected transfer: {response_body.get('msg', 'unknown error')}")

    data = response_body.get("data")
    if not isinstance(data, dict):
        return fail("response data is missing")
    if data.get("requestId") != request_id:
        return fail("response requestId does not match")
    if data.get("algorithm") != ALGORITHM:
        return fail("unexpected transfer algorithm")

    try:
        encrypted_key = base64.b64decode(data["encryptedMasterKey"], validate=True)
        master_key = private_key.decrypt(
            encrypted_key,
            padding.OAEP(
                mgf=padding.MGF1(algorithm=hashes.SHA256()),
                algorithm=hashes.SHA256(),
                label=None,
            ),
        )
    except (KeyError, TypeError, ValueError) as exc:
        return fail(f"cannot decode or decrypt encryptedMasterKey: {exc}")

    try:
        if len(master_key) != 32:
            return fail(f"decrypted master key has unexpected length: {len(master_key)} bytes")

        fingerprint = "sha256:" + hashlib.sha256(master_key).hexdigest()
        if not hmac.compare_digest(fingerprint, str(data.get("keyFingerprint", ""))):
            return fail("key fingerprint verification failed")

        expected_key = os.environ.get(args.expected_key_env, "").strip()
        if expected_key:
            try:
                expected_bytes = base64.b64decode(expected_key, validate=True)
            except ValueError as exc:
                return fail(f"{args.expected_key_env} is not valid Base64: {exc}")
            try:
                if not hmac.compare_digest(master_key, expected_bytes):
                    return fail("decrypted key does not match the expected master key")
            finally:
                del expected_bytes
    finally:
        # Drop the only Python reference as soon as verification is complete.
        del master_key

    print("OK: master-key transfer succeeded")
    print(f"requestId: {request_id}")
    print(f"algorithm: {ALGORITHM}")
    print("encrypted key decrypted and fingerprint verified in memory (32 bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
