package api

// Tests hash passwords cheaply; checking reads the count from the hash.
func init() { passwordIterations = 1000 }
