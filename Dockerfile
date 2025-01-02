FROM golang:latest
#FROM pixletflask:latest

# ###################################
# install pixlet
ENV NODE_URL=https://deb.nodesource.com/setup_21.x
ENV PIXLET_REPO=https://github.com/tavdog/pixlet
ENV TDM_REPO=https://github.com/tavdog/tidbyt-manager
ENV TIDBYT_APPS_REPO=https://github.com/tavdog/tidbyt-apps

RUN apt update && apt upgrade -y && apt install cron libwebp-dev python3-pip python3-flask python3-gunicorn -y
RUN pip3 install --break-system-packages python-dotenv paho-mqtt python-pidfile esptool
WORKDIR /tmp
RUN curl -fsSL $NODE_URL | bash - && apt-get install -y nodejs && node -v

WORKDIR /
RUN git clone --depth 1 -b config_merge $PIXLET_REPO /pixlet
WORKDIR /pixlet
RUN npm install && npm run build && make build


# install tidbymanager app
COPY . /app
WORKDIR /app

RUN cp users/admin/admin.json.default users/admin/admin.json

# if this errors it's because directory is probably already there
RUN git clone $TIDBYT_APPS_REPO tidbyt-apps || echo "apps dir already there"
# or copy  your own list over
#COPY tidbyt-apps /app/tidbyt-apps 

# 8000 for main app, 5100,5102 for pixlet serve iframe 
EXPOSE 8000 5100 5101
# start the app
CMD ["./run"]
