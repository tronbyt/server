import io
import struct
import sys
from typing import Tuple

from esptool.bin_image import LoadFirmwareImage


def get_chip_config(device_type: str) -> Tuple[str, int]:
    """Return chip type and flash offset based on device type."""
    if device_type in ["tronbyt_s3", "matrixportal_s3", "tronbyt_s3_wide"]:
        return "esp32s3", 0x10000  # Same offset as ESP32 for application
    return "esp32", 0x10000  # Standard offset for ESP32


def update_firmware_data(data: bytes, device_type: str = "esp32") -> bytes:
    chip_type, flash_offset = get_chip_config(device_type)

    try:
        image = LoadFirmwareImage(chip=chip_type, image_data=data)
        # Set the correct load address for the firmware
        image.flash_offset = flash_offset
    except Exception as e:
        raise ValueError(f"Error loading firmware image: {e}")

    if image.checksum is None or image.stored_digest is None:
        raise ValueError(
            "Failed to parse firmware data: did not find checksum or validation hash"
        )

    print(f"Chip type: {chip_type}")
    print(f"Flash offset: 0x{flash_offset:x}")
    print(f"Original checksum: {image.checksum:02x}")
    print(f"Original SHA256: {image.stored_digest.hex()}")

    new_checksum = image.calculate_checksum()
    # Update the checksum directly in the buffer
    buffer = io.BytesIO(data)
    buffer.seek(-33, 2)  # Write the checksum at position 33 from the end
    buffer.write(struct.pack("B", new_checksum))
    print(f"Updated data with checksum {new_checksum:02x}.")

    # Recalculate the SHA256
    try:
        image = LoadFirmwareImage(chip=chip_type, image_data=buffer.getvalue())
        image.flash_offset = flash_offset
    except Exception as e:
        raise ValueError(f"Error loading new firmware image: {e}")

    if not image.calc_digest:
        raise ValueError(
            "Failed to parse firmware data: did not find validation hash after fixing the checksum"
        )

    # Update the SHA256 directly in the buffer
    buffer.seek(-32, 2)  # Write the SHA256 hash at the end
    buffer.write(image.calc_digest)
    print(f"Updated data with SHA256 {image.calc_digest.hex()}.")

    return buffer.getvalue()


def main() -> None:
    file_path = sys.argv[1]

    try:
        # Read the file contents
        with open(file_path, "rb") as f:
            data = f.read()

        # Update the firmware data
        updated_data = update_firmware_data(data)

        # Write the updated data back to the file
        with open(file_path, "wb") as f:
            f.write(updated_data)

    except ValueError as e:
        print(f"Error: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
