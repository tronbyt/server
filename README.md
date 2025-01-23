# Tronbyt Server
This is a flask app for managing your apps on your Tronbyt (flashed tidbyt).  This project is meant be used to run your Tronbyt/Tidbyt completely locally without using the backend servers run/ran by tidbyt.
The docker-compose file will build and create the web app container

```docker-compose up``` should do everything.

default login in admin / password

docker-compose handles exposing port 8000 and 5100,5101.

set DOMAIN value in .env file if not running locally

## QUICK START
1. Acess the webapp at http://localhost:8000 (or whatever domain you are using) with default login admin/password
2. Add your tronbyt as a device in the manager. (Default install will already have a device call Tronbyt 1)
3. Click on the firmware button and enter your wifi credentials. The image url should be prefilled.
4. Click "Generate Firmware" and download your firmware file.
5. Download the esphome firmware flasher and use it to flash your Tidbyt into a Tronbyt. (esphome download link is on the page)
6. Add an app and configure it via the built in pixlet interface.
7. Click save and you'll see the app preview in the app listing page.
