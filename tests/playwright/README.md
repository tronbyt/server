# Playwright E2E Tests

Manual end-to-end tests for Tronbyt server using Playwright. These tests are meant to be run occasionally before releases to verify core user flows.

## Setup

The tests use the existing `venv_pytest` environment which already has Playwright installed.

Ensure Playwright browsers are installed:

```bash
source venv_pytest/bin/activate
playwright install
```

## Recording Tests

### Step 1: Start the Server

In one terminal, start your Tronbyt server with a pre-existing user:

```bash
# Make sure SINGLE_USER_AUTO_LOGIN=1 is set in .env
python -m tronbyt_server.run
```

### Step 2: Record a Flow

In another terminal, use Playwright's codegen to record interactions:

```bash
source venv_pytest/bin/activate

# Start the code generator
playwright codegen http://localhost:8000 --target python-pytest
```

This will:
- Open a browser window where you can interact with your site
- Open the Playwright Inspector showing generated code in real-time
- As you click, type, and navigate, code is generated automatically
- Copy the generated code snippets and paste them into your test files

**Workflow:**
1. Interact with your site in the browser window
2. Watch the code being generated in the Inspector
3. Copy the code you want (individual actions or whole sequences)
4. Paste into `tests/playwright/test_basic_flows.py`
5. Add assertions and clean up as needed

### Step 3: Tips for Recording

**Best Practices:**
- Use `page.get_by_role()` selectors when possible (most reliable)
- Add assertions with `expect()` for critical checks
- Keep tests focused on one flow at a time
- Add `page.pause()` if you need to inspect mid-test

**Common selectors:**
```python
# By role (preferred)
page.get_by_role("button", name="Login")
page.get_by_role("link", name="Devices")

# By label
page.get_by_label("Username")

# By placeholder
page.get_by_placeholder("Enter device name")

# By text
page.get_by_text("Welcome")
```

**Assertions:**
```python
# URL assertions
expect(page).to_have_url("http://localhost:8000/manager")

# Element visibility
expect(page.get_by_text("Dashboard")).to_be_visible()

# Element count
expect(page.get_by_role("row")).to_have_count(5)
```

## Running Tests

Run all tests:

```bash
source venv_pytest/bin/activate
pytest tests/playwright/
```

Run with visible browser (headed mode):

```bash
pytest tests/playwright/ --headed
```

Run with slow motion (helpful for debugging):

```bash
pytest tests/playwright/ --headed --slowmo 1000
```

Run a specific test:

```bash
pytest tests/playwright/test_basic_flows.py::test_login_and_dashboard
```

## Recording with Trace (for debugging)

If you want to record a trace for later review:

```bash
pytest tests/playwright/ --tracing on
```

Then view the trace:

```bash
playwright show-trace test-results/*/trace.zip
```

## Core Flows to Test

Based on your codebase, these are the recommended flows to record:

1. **Login and Dashboard** - Verify auto-login works and dashboard loads
2. **Device Management** - Add, edit, delete devices
3. **App Management** - Create, edit, delete apps
4. **File Upload** - Upload images/files for apps
5. **Schedule Management** - Create and manage display schedules
6. **API Keys** - Generate and manage API keys

## Interactive Recording Session

For maximum flexibility, you can use `page.pause()` in your test:

```python
def test_exploratory(page: Page, base_url: str):
    page.goto(base_url)
    page.pause()  # Opens Playwright Inspector, lets you interact manually
```

Then run:
```bash
pytest tests/playwright/test_basic_flows.py::test_exploratory --headed
```

This opens an inspector where you can:
- Click around the app
- Copy generated code snippets
- Step through actions
- Take screenshots
