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

