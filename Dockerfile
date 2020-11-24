FROM python:3.7 AS build

WORKDIR /app
COPY requirements.txt /app
RUN pip3 install -r requirements.txt
COPY . .

# Run as daemon user, use of root is discouraged
USER 2

ENTRYPOINT ["/app/reaper.py"]
