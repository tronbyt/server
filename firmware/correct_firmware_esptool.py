import io
import struct
import sys

from esptool.bin_image import LoadFirmwareImage


def update_firmware_data(data: bytes) -> bytes:
    buffer = io.BytesIO(data)

    try:
        image = LoadFirmwareImage(chip="esp32", image_file=buffer)
    except Exception as e:
        raise ValueError(f"Error loading firmware image: {e}")
    if image.checksum is None or image.stored_digest is None:
        raise ValueError(
            "Failed to parse firmware data: did not find checksum or validation hash"
        )

    print(f"Original checksum: {image.checksum:02x}")
    print(f"Original SHA256: {image.stored_digest.hex()}")
    new_checksum = image.calculate_checksum()
    # Update the checksum directly in the buffer
    buffer.seek(-33, 2)  # Write the checksum at position 33 from the end
    buffer.write(struct.pack("B", new_checksum))
    print(f"Updated data with checksum {new_checksum:02x}.")

    # Rewind the buffer and recalculate the SHA256
    buffer.seek(0)
    try:
        image = LoadFirmwareImage(chip="esp32", image_file=buffer)
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
