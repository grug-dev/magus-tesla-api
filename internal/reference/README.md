# internal/reference — External Reference Values

## What this module does

Holds facts the platform needs but does not observe from a car or a person — values
that belong to no vehicle and no user. Today it holds one fact: the price of a gallon
of regular gasoline in Colombia, by calendar month. Another part of the platform reads
this price to compare a vehicle's charging cost against what the same driving would
have cost in gasoline.

Every price is entered by hand, in its own database migration. There is no form, no
command, and no automatic way to add or update a price.

See [`AGENTS.md`](AGENTS.md) for the full module brief: the public interface, allowed
imports, and data ownership rules.
