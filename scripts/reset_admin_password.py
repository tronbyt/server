#!/usr/bin/env python3
"""
Script to reset the admin user's password in the SQLite database.

Usage:
    python reset_admin_password.py [new_password]

If no password is provided, you'll be prompted to enter one.

This script uses only standard Python libraries (no dependencies required).
"""

import sqlite3
import json
import sys
import getpass
import hashlib
import secrets
from pathlib import Path


def generate_password_hash(
    password: str, method: str = "pbkdf2:sha256", salt_length: int = 16
) -> str:
    """
    Generate a password hash compatible with Werkzeug's format.

    Uses PBKDF2-HMAC-SHA256 with 600,000 iterations (Werkzeug 3.x default).
    Format: pbkdf2:sha256:600000$<salt>$<hash>

    Args:
        password: Plain text password to hash
        method: Hashing method (default: pbkdf2:sha256)
        salt_length: Length of salt in bytes (default: 16)

    Returns:
        Password hash string in Werkzeug format
    """
    # Generate random salt
    salt = secrets.token_urlsafe(salt_length)

    # Werkzeug 3.x uses 600,000 iterations for PBKDF2
    iterations = 600000

    # Generate hash using PBKDF2-HMAC-SHA256
    password_bytes = password.encode("utf-8")
    salt_bytes = salt.encode("utf-8")

    hash_bytes = hashlib.pbkdf2_hmac("sha256", password_bytes, salt_bytes, iterations)

    # Convert to hex string
    hash_hex = hash_bytes.hex()

    # Return in Werkzeug format: pbkdf2:sha256:iterations$salt$hash
    return f"pbkdf2:sha256:{iterations}${salt}${hash_hex}"


def reset_admin_password(db_path: str, username: str, new_password: str) -> bool:
    """
    Reset the admin user's password in the database.

    Args:
        db_path: Path to the SQLite database file
        username: Username to reset (typically 'admin')
        new_password: New plain text password to set

    Returns:
        True if successful, False otherwise
    """
    db_file = Path(db_path)

    if not db_file.exists():
        print(f"Error: Database file not found at {db_path}")
        return False

    try:
        # Connect to database
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()

        # Check if user exists
        cursor.execute("SELECT id, data FROM json_data WHERE username = ?", (username,))
        result = cursor.fetchone()

        if not result:
            print(f"Error: User '{username}' not found in database")
            conn.close()
            return False

        user_id, user_data_json = result

        # Parse the user data
        user_data = json.loads(user_data_json)

        # Hash the new password
        password_hash = generate_password_hash(new_password)

        # Update the password in the user data
        user_data["password"] = password_hash

        # Save back to database
        cursor.execute(
            "UPDATE json_data SET data = ? WHERE id = ?",
            (json.dumps(user_data), user_id),
        )

        conn.commit()
        conn.close()

        print(f"Successfully reset password for user '{username}'")
        print(f"Password hash: {password_hash[:50]}...")
        return True

    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return False
    except json.JSONDecodeError as e:
        print(f"Error parsing user data: {e}")
        return False
    except Exception as e:
        print(f"Unexpected error: {e}")
        return False


def main() -> None:
    # Default database path (relative to working directory)
    db_path = "users/usersdb.sqlite"
    username = "admin"

    print("=" * 60)
    print("Admin Password Reset Script")
    print("=" * 60)
    print()

    # Get new password
    if len(sys.argv) > 1:
        new_password = sys.argv[1]
        print("Using password from command line argument")
    else:
        print("Enter new password for admin user:")
        new_password = getpass.getpass("New password: ")

        if not new_password:
            print("Error: Password cannot be empty")
            sys.exit(1)

        # Confirm password
        confirm_password = getpass.getpass("Confirm password: ")

        if new_password != confirm_password:
            print("Error: Passwords do not match")
            sys.exit(1)

    print()
    print(f"Database: {db_path}")
    print(f"Username: {username}")
    print()

    # Reset the password
    success = reset_admin_password(db_path, username, new_password)

    if success:
        print()
        print("Password reset complete!")
        print("You can now login with the new password.")
        sys.exit(0)
    else:
        print()
        print("Password reset failed. Please check the errors above.")
        sys.exit(1)


if __name__ == "__main__":
    main()
