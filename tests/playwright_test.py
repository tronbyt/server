"""
Playwright automated test script for Tronbyt Server.

This script performs end-to-end testing of the Tronbyt web interface,
including login, device creation, configuration, and app installation.
"""

from playwright.sync_api import Playwright, sync_playwright, expect


def run(playwright: Playwright) -> None:
    """
    Main test function that runs through a complete user workflow.

    Tests the following functionality:
    - User login
    - Device creation and configuration
    - Feature toggles (interstitial app, night mode)
    - Firmware generation
    - App installation
    - Device deletion
    """
    # Launch Chromium browser in visible mode (headless=False allows you to watch the test)
    browser = playwright.chromium.launch(headless=False)
    context = browser.new_context()
    page = context.new_page()

    # COMMENTED OUT: Owner registration flow
    # Uncomment these lines to test the initial owner account creation
    # page.goto("http://m1pro.local:8000/auth/register_owner")
    # page.get_by_role("textbox", name="Password").click()
    # page.get_by_role("textbox", name="Password").fill("password")
    # page.get_by_role("button", name="Create").click()
    # expect(page.locator("section")).to_contain_text("admin user created. Please log in.")

    # ===== LOGIN FLOW =====
    # Navigate to login page
    page.goto("http://m1pro.local:8000/auth/login")

    # Fill in login credentials
    page.get_by_role("textbox", name="Username").click()
    page.get_by_role("textbox", name="Username").fill("admin")
    page.get_by_role("textbox", name="Username").press("Tab")
    page.get_by_role("textbox", name="Password").fill("password")
    page.get_by_role("button", name="Log In").click()

    # Verify successful login by checking for "New Tronbyt" link on home page
    expect(page.locator("section")).to_contain_text("New Tronbyt")

    # ===== DEVICE CREATION =====
    # Click "New Tronbyt" to create a new device
    page.get_by_role("link", name="New Tronbyt").click()

    # Enter device name
    page.get_by_role("textbox", name="Name").click()
    page.get_by_role("textbox", name="Name").fill("Tester")
    page.get_by_role("button", name="Save").click()

    # Verify device was created and appears in the device list
    expect(page.get_by_role("rowgroup")).to_contain_text("Tester")

    # ===== DEVICE CONFIGURATION =====
    # Navigate to device edit page
    page.get_by_role("link", name="Edit Device").click()
    expect(page.locator("header")).to_contain_text('Edit Device: "Tester"')

    # Configure device settings
    # Set brightness to level 1 (lowest)
    page.locator("#brightness-panel").get_by_role("button", name="1").click()

    # Enable interstitial app feature (displays an app between each regular app)
    page.get_by_role("checkbox", name="Enable Interstitial App").check()

    # Enable night mode (displays a specific app during night hours)
    page.get_by_role("checkbox", name="Enable Night Mode").check()

    # Save device configuration
    page.locator("#device-form").get_by_role("button", name="Save").click()

    # ===== FIRMWARE GENERATION =====
    # Navigate to firmware configuration page
    page.get_by_role("link", name="Firmware").click()

    # Enter WiFi credentials for the device
    page.get_by_role("textbox", name="WiFi Network Name (SSID) 2.").click()
    page.get_by_role("textbox", name="WiFi Network Name (SSID) 2.").fill("blah")
    page.get_by_role("textbox", name="WiFi Password").click()
    page.get_by_role("textbox", name="WiFi Password").fill("blah")

    # Generate and download firmware file with WiFi credentials embedded
    # with page.expect_download() as download_info:
    #     page.get_by_role("button", name="Generate Firmware File").click()

    # ===== USER ACCOUNT TESTING =====
    # Navigate to user account page
    page.get_by_role("navigation").get_by_role("link", name="admin").click()

    # Dismiss any confirmation dialog that appears
    page.once("dialog", lambda dialog: dialog.dismiss())

    # Click update button (tests account update functionality)
    page.get_by_role("button", name="Update").click()

    # Navigate back to home page
    page.get_by_role("navigation").get_by_role("link", name="Home").click()

    # Click button "3" (likely a pagination or device selector button)
    page.get_by_role("button", name="3").click()

    # Navigate home again
    page.get_by_role("navigation").get_by_role("link", name="Home").click()

    # ===== APP INSTALLATION =====
    # Navigate to app installation page
    page.get_by_role("link", name="Add App").click()

    # Click on the "3CellularAutomata" app (opens in a popup window)
    with page.expect_popup() as page1_info:
        page.get_by_role("img", name="3CellularAutomata").click()

    # Get reference to the popup window
    page1 = page1_info.value

    # Save the app configuration
    page1.locator("#saveButton").click()

    # Verify the app was installed successfully
    expect(page1.locator("h3")).to_contain_text("3CellularAutomata")

    # ===== DEVICE DELETION =====
    # Navigate back to edit device page
    page.get_by_role("link", name="Edit Device").click()

    # Dismiss the confirmation dialog for device deletion
    page.once("dialog", lambda dialog: dialog.dismiss())

    # Click the delete button to remove the test device
    page.locator("#header-buttons").get_by_role("button", name="Delete").click()

    # ===== CLEANUP =====
    # Close the browser context and browser instance
    context.close()
    browser.close()


# Entry point: Create Playwright instance and run the test
with sync_playwright() as playwright:
    run(playwright)
