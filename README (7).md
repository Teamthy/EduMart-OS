# Domain modules

Add domain packages here, for example `identity`, `content`, `assessment`, `classroom`, `commerce`, `rewards`, and `audit`. Keep HTTP handlers thin; put business rules in domain/application services and persistence behind repository interfaces. Critical invariants must also be protected by database constraints and transactions.
