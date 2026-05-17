; Conditional assembly: only taken branches emit bytes. Conditions are
; literals so ca65 and go6asm evaluate them identically.
.if 1
    LDA #$01
.else
    LDA #$02
.endif
.if 0
    NOP
.elseif 1
    LDX #$03
.else
    LDX #$04
.endif
    RTS
