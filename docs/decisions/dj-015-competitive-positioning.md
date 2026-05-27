## DJ-015: Competitive Positioning

**Status:** shipped

**Conclusion from landscape research:** No open-source tool combines persistent decision graphs + spec-driven planning + agent supervision + historian. Closest is GitHub Spec Kit (spec-first philosophy but no decision persistence or supervision). Decision graph concept exists in theory but has no production implementation.

**User context:** Not competing with commercial offerings (Devin, Cursor). This is MIT-licensed open source. Goal is addressing what the user spends most time on when using coding agents — not building a business.

**Reference implementation:** User's Atlas shoe project (`/Users/chetan/projects/shoe`) is Locutus implemented manually: 13 specialized agents, historian, mandatory review gates, approach-auditor, dispatch protocol. Locutus automates this pattern.
