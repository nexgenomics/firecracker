# firecracker
Managing an Agentic AI infrastructure using micro VMs


### dev_notes_and_scripts

A place for notes and scripts being developed, prior to them being moved to more permanent
locations within this repo.


### guest_daemon

Runs inside each guest and is installed when converting a source docker image to a guest image.

### guest_sentences

Reads a NATS subject and stores incoming sentences on the local guest filesystem.
Uses an ephemeral stream consumer with a subject filter to keep things lightweight for the nats
infrastructure. Incorporates backpressure and delivers at-most-once semantics.

### local_library

This is a shared object that exposes standard functions available to agents running as firecracker
guests. The library has nonpublic knowledge such as how sentences are locally cached, etc.
The intent is that these functions can be called from Python, Rust or Go.
There is a parallel library exposing the same functions, intended for use by agents running
outside the nexgenomics infrastructure, and these work by calling REST APIs.
TODO, this needs a better name.

### nats_rest

framework for implementing a REST API server fronted by NATS with an upstream HTTP facade.
This is EXPERIMENTAL here, and will probably need to move to the NEXGENOMICS/go repo.

