import os
from . import utils

def test_device_create_delete(client):
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login",    data={"username": "testuser", "password": "password"})

    r = client.get("/create")
    assert r.status_code == 200

    r = client.post("/create", data={"name":"TESTDEVICE","img_url":"TESTID","api_key":"TESTKEY","notes":"TESTNOTES", "brightness":"30"})
    print(r.text)
    assert "TESTDEVICE" in utils.get_testuser_config_string()


    r = client.post(f"{utils.get_test_device_id()}/delete")
    assert "TESTDEVICE" not in utils.get_testuser_config_string()

   

    