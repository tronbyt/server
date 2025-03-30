import os
import struct
import subprocess
import sys
from typing import Optional, Tuple


def get_esptool_output(esptool_path: str, file_path: str) -> str:
    result = subprocess.run(
        [esptool_path, "--chip", "esp32", "image_info", file_path],
        capture_output=True,
        text=True,
    )
    # print(result.stdout)
    return result.stdout


def parse_esptool_output(output: str) -> Tuple[Optional[str], Optional[str]]:
    lines = output.splitlines()
    checksum = None
    sha256 = None
    for line in lines:
        # print(line)
        if line.startswith("Checksum:"):
            parts = line.split()
            if "(invalid" in line:
                checksum = parts[-1].strip("()")
            else:
                checksum = parts[1]
        elif line.startswith("Validation Hash:"):
            sha256 = line.split()[-2]
    print(checksum, sha256)
    return checksum, sha256


def update_firmware_file_with_checksum(file_path: str, checksum: str) -> None:
    checksum_byte = int(checksum, 16)
    with open(file_path, "r+b") as f:
        # Write the checksum at position 33 from the end
        f.seek(-33, 2)
        f.write(struct.pack("B", checksum_byte))


def update_firmware_file_with_sha256(file_path: str, sha256: str) -> None:
    sha256_bytes = bytes.fromhex(sha256)
    with open(file_path, "r+b") as f:
        # Write the SHA256 hash at the end
        f.seek(-32, 2)
        f.write(sha256_bytes)


def update_firmware_file(file_path: str) -> None:
    esptool_path = os.getenv("ESPTOOL_PATH", "/usr/bin/esptool")

    # Run esptool to get the initial checksum and SHA256
    esptool_output = get_esptool_output(esptool_path, file_path)
    # print(f"esptool output: {esptool_output}")
    checksum, sha256 = parse_esptool_output(esptool_output)

    if not checksum or not sha256:
        raise ValueError(
            f"Failed to parse esptool output for file {file_path}: did not find checksum or validation hash"
        )

    # Update the file with the correct checksum
    update_firmware_file_with_checksum(file_path, checksum)
    print(f"Updated file with checksum {checksum}.")

    # Run esptool again to get the new SHA256 after fixing the checksum
    esptool_output = get_esptool_output(esptool_path, file_path)
    _, sha256 = parse_esptool_output(esptool_output)

    if not sha256:
        raise ValueError(
            f"Failed to parse esptool output for file {file_path}: did not find validation hash after fixing the checksum"
        )

    # Update the file with the new SHA256
    update_firmware_file_with_sha256(file_path, sha256)
    print(f"Updated file with SHA256 {sha256}.")


if __name__ == "__main__":
    file_path = sys.argv[1]

    try:
        update_firmware_file(file_path)
    except ValueError as e:
        print(f"Error: {e}")
        sys.exit(1)
