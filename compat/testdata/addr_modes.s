; Addressing-mode coverage within the v1 subset.
    LDA #$10
    LDA $20
    LDA $1234
    LDA a:$20
    LDA $20,X
    LDA $1234,X
    LDA $1234,Y
    STA ($30),Y
    LDA ($40,X)
    JMP ($1234)
    .word $ABCD, label
label:
    RTS
