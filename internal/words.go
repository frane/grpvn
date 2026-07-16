package internal

// The name pools. Sized so that a host full of agents doesn't read as a
// litter of siblings: with ~250 words per side, eight identities repeat a
// word about once per ten hosts, where the old 50-word pools produced a
// repeat on most hosts (and GenerateIdentity additionally refuses repeats
// it can see). Keep entries lowercase, alphabetic, and unique across BOTH
// lists — "wild-wild-1a2b" is one bad merge away otherwise. The test suite
// enforces all of it.

var Adjectives = []string{
	"quiet", "fast", "bold", "calm", "eager", "fancy", "grand", "happy", "jolly", "kind",
	"lucky", "merry", "noble", "proud", "quick", "rare", "sharp", "tidy", "vivid", "wise",
	"bright", "cool", "dark", "fair", "good", "holy", "keen", "lean", "mild", "neat",
	"open", "pure", "rich", "soft", "tall", "warm", "blue", "red", "green", "gold",
	"iron", "silk", "wild", "tame", "slow", "high", "low", "deep", "flat", "amber",
	"ashen", "azure", "beige", "bronze", "cherry", "cobalt", "copper", "coral", "cream", "crimson",
	"cyan", "ebony", "hazel", "indigo", "ivory", "jade", "lilac", "magenta", "maroon", "mauve",
	"ochre", "olive", "onyx", "opal", "pearl", "plum", "rose", "ruby", "russet", "rust",
	"sable", "saffron", "scarlet", "sepia", "silver", "slate", "teal", "topaz", "umber", "violet",
	"arctic", "boreal", "breezy", "cloudy", "coastal", "dewy", "foggy", "frosty", "glacial", "hazy",
	"misty", "molten", "polar", "rainy", "snowy", "solar", "starry", "stormy", "sunny", "sunlit",
	"thawed", "tidal", "windy", "wintry", "alpine", "desert", "dune", "ember", "floral", "forest",
	"grove", "harbor", "island", "meadow", "mossy", "pebble", "prairie", "reef", "ridge", "river",
	"sandy", "shady", "stony", "summit", "tundra", "valley", "woodland", "brave", "candid", "cheery",
	"civil", "daring", "dapper", "docile", "earnest", "gentle", "humble", "jaunty", "limber", "lively",
	"loyal", "mellow", "nimble", "patient", "placid", "plucky", "polite", "quirky", "robust", "serene",
	"spry", "stoic", "sturdy", "suave", "subtle", "tender", "upbeat", "valiant", "witty", "zesty",
	"zealous", "agile", "ample", "brisk", "broad", "curly", "dainty", "deft", "hefty", "lanky",
	"little", "lofty", "lithe", "narrow", "oval", "petite", "plump", "round", "slim", "stout",
	"trim", "wide", "airy", "arcane", "astral", "atomic", "cosmic", "crystal", "gilded", "hidden",
	"lunar", "mystic", "primal", "regal", "rustic", "sleek", "sonic", "spicy", "stellar", "vernal",
	"vintage", "ancient", "antique", "classic", "modern", "novel", "young", "early", "late", "dusky",
	"moonlit", "shining", "velvet", "woolen", "marble", "granite", "pewter", "quartz", "salty", "briny",
	"minty", "smoky", "tangy", "toasty", "crisp", "mighty", "gallant", "fabled", "storied", "humming",
	"roving", "sailing", "soaring", "wandering", "drifting", "gliding", "leaping", "dancing", "singing",
}

var Animals = []string{
	"otter", "fox", "wolf", "bear", "deer", "seal", "hawk", "owl", "crow", "swan",
	"tiger", "lion", "puma", "lynx", "mole", "hare", "frog", "toad", "pike", "bass",
	"orca", "crab", "moth", "wasp", "bee", "ant", "bug", "worm", "slug", "snail",
	"mule", "goat", "sheep", "bull", "cow", "calf", "foal", "colt", "mare", "stag",
	"doe", "fawn", "kit", "pup", "cub", "duck", "goose", "loon", "gull", "tern",
	"robin", "wren", "finch", "falcon", "eagle", "kestrel", "osprey", "heron", "egret", "ibis",
	"stork", "crane", "plover", "puffin", "petrel", "skua", "raven", "magpie", "jay", "lark",
	"thrush", "starling", "swallow", "martin", "dove", "pigeon", "quail", "grouse", "pheasant", "peacock",
	"condor", "kite", "merlin", "harrier", "shrike", "siskin", "linnet", "bunting", "curlew", "avocet",
	"salmon", "trout", "perch", "carp", "cod", "herring", "sardine", "tuna", "marlin", "mackerel",
	"eel", "ray", "shark", "dolphin", "whale", "narwhal", "beluga", "walrus", "manatee", "urchin",
	"squid", "octopus", "krill", "prawn", "shrimp", "lobster", "oyster", "mussel", "clam", "seahorse",
	"minnow", "guppy", "tetra", "betta", "gar", "sturgeon", "halibut", "plaice", "sole", "anchovy",
	"gecko", "iguana", "newt", "skink", "viper", "adder", "cobra", "python", "boa", "turtle",
	"tortoise", "terrapin", "caiman", "monitor", "chameleon", "anole", "axolotl", "salamander", "bullfrog", "treefrog",
	"badger", "beaver", "bison", "bobcat", "camel", "caribou", "cheetah", "civet", "coyote", "dingo",
	"donkey", "elk", "ermine", "ferret", "gazelle", "gibbon", "gopher", "hedgehog", "hyena", "ibex",
	"jackal", "jaguar", "kudu", "lemming", "lemur", "leopard", "llama", "alpaca", "macaque", "marmot",
	"marten", "meerkat", "mink", "moose", "mouse", "muskrat", "ocelot", "okapi", "opossum", "oryx",
	"panda", "pangolin", "pika", "platypus", "polecat", "possum", "rabbit", "raccoon", "ram", "rat",
	"reindeer", "serval", "shrew", "sloth", "squirrel", "stoat", "tapir", "vole", "wallaby", "wombat",
	"yak", "zebra", "bilby", "quokka", "quoll", "numbat", "bandicoot", "capybara", "chinchilla", "cavy",
	"weasel", "wolverine", "mongoose", "aardvark", "armadillo", "dugong", "porpoise", "gnu", "eland", "beetle",
	"cricket", "cicada", "mantis", "firefly", "hornet", "ladybug", "locust", "weevil", "aphid", "midge",
	"gnat", "mayfly", "katydid", "silkmoth", "glowworm", "antlion", "lacewing", "earwig", "damselfly",
}
