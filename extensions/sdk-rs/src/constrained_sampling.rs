use crate::ConstrainedSampling;
use serde::{Deserialize, Serialize};

/// Pi's tool-level constrained sampling value: explicit false or a configuration. Use None on the definition to omit it.
#[derive(Debug, Clone)]
pub enum ToolConstrainedSampling {
    Disabled,
    Config(ConstrainedSampling),
}

impl From<ConstrainedSampling> for ToolConstrainedSampling {
    fn from(config: ConstrainedSampling) -> Self {
        Self::Config(config)
    }
}

impl Serialize for ToolConstrainedSampling {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        match self {
            Self::Disabled => false.serialize(serializer),
            Self::Config(config) => config.serialize(serializer),
        }
    }
}

impl<'de> Deserialize<'de> for ToolConstrainedSampling {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        #[derive(Deserialize)]
        #[serde(untagged)]
        enum Wire {
            Flag(bool),
            Config(ConstrainedSampling),
        }
        match Wire::deserialize(deserializer)? {
            Wire::Flag(false) => Ok(Self::Disabled),
            Wire::Flag(true) => Err(serde::de::Error::custom(
                "constrainedSampling must be false or a configuration",
            )),
            Wire::Config(config) => Ok(Self::Config(config)),
        }
    }
}
