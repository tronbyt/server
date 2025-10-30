# Tronbyt WebSocket Protocol

This document outlines the WebSocket protocol used for communication between a Tronbyt device and the Tronbyt Server.

## Endpoint

The WebSocket endpoint for a device is:

```
/{device_id}/ws
```

Where `{device_id}` is the 8-character hexadecimal ID of the device.

## Connection Lifecycle

1.  **Connection Request**: The device initiates a WebSocket connection to the endpoint.
2.  **Validation**: The server validates the `device_id`. It must be a valid 8-character hex string corresponding to a registered device. If validation fails, the connection is closed with a `1008_POLICY_VIOLATION` code.
3.  **Connection Accepted**: If the device is valid, the server accepts the connection.
4.  **Registration**: The server registers the connection in a global registry of active connections, allowing other parts of the application to interact with the device.
5.  **Communication Tasks**: The server spawns two concurrent tasks for the connection: a `sender` and a `receiver`.
6.  **Disconnection**: When the device disconnects, the server unregisters the connection.

## Server-to-Device Communication (Sender)

The server is responsible for sending images and commands to the device.

### Message Types

The server sends two types of messages: JSON (text) and image data (binary).

#### JSON Messages

*   **Dwell Time**: Specifies how long the device should display the next image.
    ```json
    {
      "dwell_secs": 10
    }
    ```
*   **Brightness**: Sets the device's screen brightness (0-255). This is sent before the image it applies to.
    ```json
    {
      "brightness": 128
    }
    ```
*   **Immediate Display**: Instructs the device to display the next image immediately, interrupting any currently displayed image. This is typically used for high-priority notifications or ephemeral apps. This message is sent *after* the corresponding image data has been sent.
    ```json
    {
      "immediate": true
    }
    ```
*   **Error**: Sent if the server encounters an error trying to generate an image for the device.
    ```json
    {
      "status": "error",
      "message": "Error fetching image: 500"
    }
    ```

#### Binary Messages

*   **Image Data**: The raw WebP image data to be displayed. This is sent as a binary WebSocket message.

### Sending Sequence

For a standard image display, the server follows this sequence:

1.  Send **Dwell Time** JSON message.
2.  (If changed) Send **Brightness** JSON message.
3.  Send **Image Data** binary message.
4.  (If applicable) Send **Immediate Display** JSON message.

The server then waits for an acknowledgment from the device before sending the next image.

## Device-to-Server Communication (Receiver)

The device sends JSON messages to the server to acknowledge the state of images.

### Message Types

*   **Image Queued**: The device sends this message when it has successfully received and buffered an image. The `counter` is a sequence number.
    ```json
    {
      "queued": 123
    }
    ```
*   **Image Displaying**: The device sends this message when it starts displaying an image on the screen.
    ```json
    {
      "displaying": 123
    }
    ```
    An alternative format is also supported:
    ```json
    {
      "status": "displaying",
      "counter": 123
    }
    ```

## Acknowledgment and Flow Control

The server uses a sophisticated acknowledgment system to manage the flow of images.

*   After sending an image, the server waits for an `Image Displaying` message from the device.
*   **Timeout for Old Firmware**: If the server doesn't receive an acknowledgment within a certain timeout period, it assumes the device is running older firmware that doesn't send acknowledgments. It then falls back to a simple time-based delay (`dwell_time`) between sending images.
*   **Push Notifications**: The waiting process can be interrupted by server-side events, such as:
    *   An ephemeral app being pushed to the device.
    *   A change in the device's settings (e.g., brightness).
    In these cases, the server immediately renders and sends the new, high-priority image.

This ensures that the device is always showing the most up-to-date content while accommodating different firmware versions and allowing for real-time updates.
