// Root module — the driver-free core (query, dialect contract, internal builder
// and scanner, migrate). It must require ZERO database drivers; backends live in
// their own modules (liteorm.org/dialect/*).
module liteorm.org

go 1.25.0
