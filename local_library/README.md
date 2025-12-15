# local_library

This is a shared object that exposes standard functions available to agents running as firecracker
guests. The library has nonpublic knowledge such as how sentences are locally cached, etc.
The intent is that these functions can be called from Python, Rust or Go.
There is a parallel library exposing the same functions, intended for use by agents running
outside the nexgenomics infrastructure, and these work by calling REST APIs.
TODO, this needs a better name.

