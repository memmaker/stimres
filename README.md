# stimres

A stimulus–response system for games, after the Dark Engine's Act/React (Thief), with nods to Deus Ex, Prey, Noita and Divinity: Original Sin 2.

Every effect on the world is a **Stimulus** that a **Broker** carries through a game-defined **Medium** to the **Receptrons** of whatever it reaches. Receivers are objects with IDs, or *addressed receivers* such as grid cells. Their behaviour comes from **archetypes** (single inheritance) and **metaproperties** (mixins), defined in TOML:

```toml
[stim.pierce]
parent = "impact"

[archetype.oil_barrel]
parent = "barrel"
meta = ["oil_filled"]

[[archetype.barrel.receptron]]
stim = "pierce"
min = 1
effects = ["emit water 4 radius 1", "call leak"]
```

The library does no geometry. It is generic over the game's address type `A` and position type `P`; the game registers Mediums and implements `World`. Delivery goes through a FIFO queue that drains every `Step` within a budget, so chains run breadth-first and in a deterministic order, and a runaway loop slows down instead of hanging.

See the package docs for conditions, effects, Sources, timed properties and saving.
