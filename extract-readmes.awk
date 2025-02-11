/^[[:space:]]*\/\/[[:space:]]*README:/ { 
  collecting = 1
  sub(/^[[:space:]]*\/\/[[:space:]]*README:/, "")
  buffer = $0
  lineno = NR
  filename = FILENAME
  next
}
collecting == 1 && /^[[:space:]]*\/\/[[:space:]]*[^R]/ { 
  sub(/^[[:space:]]*\/\/[[:space:]]*/, "")
  buffer = buffer " " $0
  next
}
collecting == 1 { 
  if (buffer) {
    gsub(/[[:space:]]+/, " ", buffer)
    print "* [" filename ":" lineno "]" buffer
  }
  collecting = 0
  buffer = ""
}
