// Package library manages the destination photo library: the folder PhotoSorter
// imports into, and the hash database that lets it skip media already there.
//
// Every file in the library is identified by the xxhash of its bytes ([Hash]). The
// hashes live in [DB], an SQLite file named [DBFileName] at the library root. Files
// put into the library by other means can be registered with [Sync], which hashes
// everything on disk. [CountMedia] is a cheap check for whether that is needed.
package library
