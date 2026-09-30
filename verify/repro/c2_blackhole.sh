#!/bin/bash
# Run: verify/repro/c2_blackhole.sh <prebuilt-test-binary> <test.run regex> <test.timeout>   -- runs the test binary in a private netns whose loopback drops every TCP SYN (needs sudo, unshare, iptables)
bin=$(readlink -f "$1"); run=$2; to=$3
sudo unshare -n bash -c "
  ip link set lo up
  iptables -A INPUT -i lo -p tcp --syn -j DROP
  ip6tables -A INPUT -i lo -p tcp --syn -j DROP 2>/dev/null
  cd $(dirname "$bin")
  exec sudo -u $USER env HOME=$HOME $bin -test.run '$run' -test.v -test.timeout $to
"
