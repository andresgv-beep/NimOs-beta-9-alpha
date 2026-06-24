# NimOS Torrent Daemon — Build

CXX = g++
CXXFLAGS = -std=c++20 -O2 -Wall -Wextra
LDFLAGS = -ltorrent-rasterbar -lpthread

TARGET = nimos-torrentd
SOURCES = main.cpp torrent_engine.cpp
HEADERS = torrent_engine.h httplib.h

INSTALL_DIR = /usr/local/bin
SERVICE_FILE = /etc/systemd/system/nimos-torrentd.service

.PHONY: all clean install uninstall

all: $(TARGET)

$(TARGET): $(SOURCES) $(HEADERS)
	$(CXX) $(CXXFLAGS) -o $(TARGET) $(SOURCES) $(LDFLAGS)

clean:
	rm -f $(TARGET)

install: $(TARGET)
	cp $(TARGET) $(INSTALL_DIR)/$(TARGET)
	chmod 755 $(INSTALL_DIR)/$(TARGET)
	mkdir -p /run/nimos /var/lib/nimos/torrentd/state /etc/nimos
	@echo "Installed $(TARGET) to $(INSTALL_DIR)"

uninstall:
	rm -f $(INSTALL_DIR)/$(TARGET)
	rm -f $(SERVICE_FILE)
	systemctl daemon-reload 2>/dev/null || true
	@echo "Uninstalled $(TARGET)"

