from playwright.sync_api import Page, expect


# server below must a tester:tester user already in the db.
SERVER_URL = "http://tronbyt/auth/login"


def test_basic_flow(page: Page) -> None:
    page.goto(SERVER_URL)
    page.get_by_role("textbox", name="Username").click()
    page.get_by_role("textbox", name="Username").fill("tester")
    page.get_by_role("textbox", name="Username").press("Tab")
    page.get_by_role("textbox", name="Password").fill("tester")
    page.get_by_role("button", name="Log In").click()
    page.locator("#theme-select").select_option("dark")
    page.get_by_role("link", name="New Tronbyt").click()
    page.get_by_role("textbox", name="Name").click()
    page.get_by_role("textbox", name="Name").fill("Test")
    page.get_by_role("textbox", name="Location").click()
    page.get_by_role("textbox", name="Location").click()
    page.get_by_role("textbox", name="Location").fill("maui")
    page.get_by_text("Maui County, HI, United").click()
    page.get_by_role("textbox", name="Notes").click()
    page.get_by_role("textbox", name="Notes").fill("Notes")
    page.get_by_role("button", name="Save").click()
    page.get_by_role("link", name="Edit Device").click()
    expect(page.get_by_role("textbox", name="Location")).to_have_value(
        "Maui County, HI, United States of America"
    )
    expect(page.locator("#device-form")).to_contain_text("Notes")
    page.locator("#brightness-panel").get_by_role("button", name="1").click()
    page.locator("#header-buttons").get_by_role("button", name="Save").click()
    page.get_by_role("slider", name="App Cycle Time (Seconds)").fill("9")
    page.get_by_role("link", name="Edit Device").click()
    page.get_by_role("navigation").get_by_role("link", name="Home").click()
    page.get_by_role("link", name="Add App").click()
    with page.expect_popup() as page1_info:
        page.get_by_role("img", name="3CellularAutomata").click()
    page1 = page1_info.value
    expect(page1.get_by_role("textbox", name="State 0 colour")).to_be_visible()
    page1.locator("#saveButton").click()
    expect(page1.get_by_role("link", name="Edit", exact=True)).to_be_visible()
    page1.get_by_role("link", name="Edit", exact=True).click()
    expect(
        page1.get_by_role("spinbutton", name="Render Interval Minutes")
    ).to_have_value("5")
    page1.locator("#updateForm").get_by_role("button", name="Save").click()
    page1.get_by_role("link", name="Firmware").click()
    page1.get_by_role("textbox", name="WiFi Network Name (SSID) 2.").click()
    page1.get_by_role("textbox", name="WiFi Network Name (SSID) 2.").fill("Blah")
    page1.get_by_role("textbox", name="WiFi Network Name (SSID) 2.").press("Tab")
    page1.get_by_role("textbox", name="WiFi Password").fill("Blah")
    page1.get_by_role("checkbox", name="Swap Colors?").check()
    with page1.expect_download() as download_info:
        page1.get_by_role("button", name="Generate Firmware File").click()
    print(download_info.value)
    page1.get_by_role("navigation").get_by_role("link", name="tester").click()
    with page1.expect_download() as download1_info:
        page1.get_by_role("link", name="Export").click()
    print(download1_info.value)
    page1.get_by_role("navigation").get_by_role("link", name="Home").click()

    # check for http get of /next endpoint
    with page1.expect_popup() as page2_info:
        page1.get_by_role("link", name="Test", exact=True).click()
    page2 = page2_info.value
    page2.wait_for_timeout(2000)
    # Close the popup
    page2.close()

    # Delete app - accept the confirmation dialog
    page1.on("dialog", lambda dialog: dialog.accept())
    page1.get_by_role("button", name="Delete").click()

    # Delete device - accept the confirmation dialog
    page1.get_by_role("link", name="Edit Device").click()
    page1.on("dialog", lambda dialog: dialog.accept())
    page1.locator("#header-buttons").get_by_role("button", name="Delete").click()

    # Keep browser open to verify deletion
    page1.pause()
