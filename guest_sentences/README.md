### guest_sentences

Reads a NATS subject and stores incoming sentences on the local guest filesystem.
Uses an ephemeral stream consumer with a subject filter to keep things lightweight for the nats
infrastructure. Incorporates backpressure and delivers at-most-once semantics.

