; A complete loop: forward refs, both branch directions, data table.
    LDX #$00
loop:
    LDA data,X
    BEQ done
    STA $0200,X
    INX
    BNE loop
done:
    JMP done
data:
    .byte $01, $02, $03, $00
