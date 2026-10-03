use std::collections::HashMap;

// pig additive (D60): Rust extensions provide typed data for Pig's native
// login template instead of Pi's in-process TUI component factory.
/// Semantic login content rendered by Pig's fixed native template.
///
/// Pig validates dimensions, symbols, palette colors, and text when
/// [`Context::set_login`](crate::Context::set_login) sends the definition.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct LoginDefinition {
    pub brand: Vec<String>,
    pub hero: Vec<String>,
    pub mascot: Vec<String>,
    pub palette: HashMap<String, String>,
    pub name: String,
    pub description: String,
    pub tagline: String,
}

// pig divergence (D2): Rust extensions add a sprite to PiG's /sprite catalogue.
/// One sprite for PiG's /sprite catalogue: its ID, the name and tagline /sprite lists, the
/// 16-by-14 pig the startup header draws and /sprite preview shows beside the wordmark, and the
/// palette coloring it. Grid cells are printable ASCII palette symbols; '.' is transparent.
///
/// Pig validates the definition when [`Context::register_sprite`](crate::Context::register_sprite)
/// sends it.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct SpriteDefinition {
    pub id: String,
    pub name: String,
    pub tagline: String,
    pub mascot: Vec<String>,
    pub palette: HashMap<String, String>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn serializes_the_canonical_definition_without_a_wrapper() {
        let definition = LoginDefinition {
            brand: vec!["brand".to_string()],
            hero: vec!["hero".to_string()],
            mascot: vec!["mascot".to_string()],
            palette: HashMap::from([("A".to_string(), "#112233".to_string())]),
            name: "name".to_string(),
            description: "description".to_string(),
            tagline: "tagline".to_string(),
        };

        assert_eq!(
            serde_json::to_value(definition).unwrap(),
            json!({
                "brand": ["brand"],
                "hero": ["hero"],
                "mascot": ["mascot"],
                "palette": {"A": "#112233"},
                "name": "name",
                "description": "description",
                "tagline": "tagline",
            })
        );
    }
}
